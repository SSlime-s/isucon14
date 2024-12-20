package main

import (
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/catatsuy/cache"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

var db *sqlx.DB

type ChairLocationCached struct {
	ChairID   string
	Latitude  int
	Longitude int
	UpdatedAt time.Time
}

var (
	LatestChairLocation = cache.NewWriteHeavyCache[string, ChairLocationCached]()
	TotalDistance       = cache.NewWriteHeavyCacheInteger[string, int]()
)

func main() {
	mux := setup()
	slog.Info("Listening on :8080")
	http.ListenAndServe(":8080", mux)
}

func setup() http.Handler {
	host := os.Getenv("ISUCON_DB_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("ISUCON_DB_PORT")
	if port == "" {
		port = "3306"
	}
	_, err := strconv.Atoi(port)
	if err != nil {
		panic(fmt.Sprintf("failed to convert DB port number from ISUCON_DB_PORT environment variable into int: %v", err))
	}
	user := os.Getenv("ISUCON_DB_USER")
	if user == "" {
		user = "isucon"
	}
	password := os.Getenv("ISUCON_DB_PASSWORD")
	if password == "" {
		password = "isucon"
	}
	dbname := os.Getenv("ISUCON_DB_NAME")
	if dbname == "" {
		dbname = "isuride"
	}

	dbConfig := mysql.NewConfig()
	dbConfig.User = user
	dbConfig.Passwd = password
	dbConfig.Addr = net.JoinHostPort(host, port)
	dbConfig.Net = "tcp"
	dbConfig.DBName = dbname
	dbConfig.ParseTime = true
	dbConfig.InterpolateParams = true

	_db, err := sqlx.Connect("mysql", dbConfig.FormatDSN())
	if err != nil {
		panic(err)
	}
	db = _db

	db.SetMaxOpenConns(1024)
	db.SetMaxIdleConns(1024)

	http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = 1024
	http.DefaultTransport.(*http.Transport).MaxIdleConns = 0 // infinity

	mux := chi.NewRouter()
	mux.Use(middleware.Logger)
	mux.Use(middleware.Recoverer)
	mux.HandleFunc("POST /api/initialize", postInitialize)

	// app handlers
	{
		mux.HandleFunc("POST /api/app/users", appPostUsers)

		authedMux := mux.With(appAuthMiddleware)
		authedMux.HandleFunc("POST /api/app/payment-methods", appPostPaymentMethods)
		authedMux.HandleFunc("GET /api/app/rides", appGetRides)
		authedMux.HandleFunc("POST /api/app/rides", appPostRides)
		authedMux.HandleFunc("POST /api/app/rides/estimated-fare", appPostRidesEstimatedFare)
		authedMux.HandleFunc("POST /api/app/rides/{ride_id}/evaluation", appPostRideEvaluatation)
		authedMux.HandleFunc("GET /api/app/notification", appGetNotification)
		authedMux.HandleFunc("GET /api/app/nearby-chairs", appGetNearbyChairs)
	}

	// owner handlers
	{
		mux.HandleFunc("POST /api/owner/owners", ownerPostOwners)

		authedMux := mux.With(ownerAuthMiddleware)
		authedMux.HandleFunc("GET /api/owner/sales", ownerGetSales)
		authedMux.HandleFunc("GET /api/owner/chairs", ownerGetChairs)
	}

	// chair handlers
	{
		mux.HandleFunc("POST /api/chair/chairs", chairPostChairs)

		authedMux := mux.With(chairAuthMiddleware)
		authedMux.HandleFunc("POST /api/chair/activity", chairPostActivity)
		authedMux.HandleFunc("POST /api/chair/coordinate", chairPostCoordinate)
		authedMux.HandleFunc("GET /api/chair/notification", chairGetNotification)
		authedMux.HandleFunc("POST /api/chair/rides/{ride_id}/status", chairPostRideStatus)
	}

	// internal handlers
	{
		mux.HandleFunc("GET /api/internal/matching", internalGetMatching)
	}

	return mux
}

type postInitializeRequest struct {
	PaymentServer string `json:"payment_server"`
}

type postInitializeResponse struct {
	Language string `json:"language"`
}

func postInitialize(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req := &postInitializeRequest{}
	if err := bindJSON(r, req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if out, err := exec.Command("../sql/init.sh").CombinedOutput(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to initialize: %s: %w", string(out), err))
		return
	}

	// if ok := migrationTotalDistance(w, r); !ok {
	// 	return
	// }
	calcCache(w, r)

	if _, err := db.ExecContext(ctx, "UPDATE settings SET value = ? WHERE name = 'payment_gateway_url'", req.PaymentServer); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, postInitializeResponse{Language: "go"})
}

func calcCache(w http.ResponseWriter, r *http.Request) {
	locations := []ChairLocation{}
	if err := db.Select(&locations, "SELECT * FROM chair_locations ORDER BY created_at"); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	totalDistances := map[string]int{}
	lastLocation := map[string]ChairLocationCached{}
	for _, location := range locations {
		distance := 0
		last, ok := lastLocation[location.ChairID]
		if !ok {
			lastLocation[location.ChairID] = ChairLocationCached{ChairID: location.ChairID, Latitude: location.Latitude, Longitude: location.Longitude, UpdatedAt: location.CreatedAt}
		} else {
			distance = abs(location.Latitude-last.Latitude) + abs(location.Longitude-last.Longitude)
			lastLocation[location.ChairID] = ChairLocationCached{ChairID: location.ChairID, Latitude: location.Latitude, Longitude: location.Longitude, UpdatedAt: location.CreatedAt}
		}

		if _, ok := totalDistances[location.ChairID]; !ok {
			totalDistances[location.ChairID] = distance
		} else {
			totalDistances[location.ChairID] += distance
		}
	}

	LatestChairLocation.SetItems(lastLocation)
	TotalDistance.SetItems(totalDistances)
}

func migrationTotalDistance(w http.ResponseWriter, r *http.Request) (ok bool) {
	// chair_locations の情報を下に、chair_total_distances に集計する

	locations := []ChairLocation{}
	if err := db.Select(&locations, "SELECT * FROM chair_locations ORDER BY created_at"); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return false
	}

	totalDistances := map[string]ChairTotalDistance{}
	lastLocation := map[string]Coordinate{}
	for _, location := range locations {
		distance := 0
		last, ok := lastLocation[location.ChairID]
		if !ok {
			lastLocation[location.ChairID] = Coordinate{Latitude: location.Latitude, Longitude: location.Longitude}
		} else {
			distance = abs(location.Latitude-last.Latitude) + abs(location.Longitude-last.Longitude)
			lastLocation[location.ChairID] = Coordinate{Latitude: location.Latitude, Longitude: location.Longitude}
		}

		if _, ok := totalDistances[location.ChairID]; !ok {
			totalDistances[location.ChairID] = ChairTotalDistance{
				ChairID:   location.ChairID,
				Distance:  distance,
				UpdatedAt: location.CreatedAt,
			}
		} else {
			totalDistances[location.ChairID] = ChairTotalDistance{
				ChairID:   location.ChairID,
				Distance:  totalDistances[location.ChairID].Distance + distance,
				UpdatedAt: location.CreatedAt,
			}
		}
	}

	totalDistancesSlice := []ChairTotalDistance{}
	for _, totalDistance := range totalDistances {
		totalDistancesSlice = append(totalDistancesSlice, totalDistance)
	}
	if _, err := db.NamedExec("INSERT INTO chair_total_distances (chair_id, distance, updated_at) VALUES (:chair_id, :distance, :updated_at)", totalDistancesSlice); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return false
	}

	return true
}

type Coordinate struct {
	Latitude  int `json:"latitude"`
	Longitude int `json:"longitude"`
}

func bindJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func writeJSON(w http.ResponseWriter, statusCode int, v interface{}) {
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	buf, err := json.Marshal(v)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(statusCode)
	w.Write(buf)
}

func writeError(w http.ResponseWriter, statusCode int, err error) {
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	w.WriteHeader(statusCode)
	buf, marshalError := json.Marshal(map[string]string{"message": err.Error()})
	if marshalError != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"marshaling error failed"}`))
		return
	}
	w.Write(buf)

	slog.Error("error response wrote", err)
}

func secureRandomStr(b int) string {
	k := make([]byte, b)
	if _, err := crand.Read(k); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", k)
}
