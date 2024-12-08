package main

import (
	"log/slog"
	"math"
	"net/http"

	"github.com/jmoiron/sqlx"
)

type checkEmpties struct {
	ChairID string `db:"chair_id"`
	Empty   bool   `db:"is_empty"`
}
type chairIdWithModel struct {
	ID    string `db:"chair_id"`
	Speed int    `db:"speed"`
}
type ChairIdAndRideId struct {
	ChairID string `db:"chair_id"`
	RideID  string `db:"ride_id"`
}

// このAPIをインスタンス内から一定間隔で叩かせることで、椅子とライドをマッチングさせる
func internalGetMatching(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	slog.Info("startMatching")
	// MEMO: 一旦最も待たせているリクエストに適当な空いている椅子マッチさせる実装とする。おそらくもっといい方法があるはず…
	rides := []Ride{}
	if err := db.SelectContext(ctx, &rides, `SELECT * FROM rides WHERE chair_id IS NULL ORDER BY created_at LIMIT 100`); err != nil {
		writeError(w, http.StatusInternalServerError, err)
	}
	if len(rides) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	chairs := []chairIdWithModel{}
	if err := db.SelectContext(ctx, &chairs, `SELECT id AS chair_id, speed FROM chairs LEFT JOIN chair_models ON chairs.model = chair_models.name WHERE is_active = TRUE`); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	chairIdToChair := map[string]chairIdWithModel{}
	for _, chair := range chairs {
		chairIdToChair[chair.ID] = chair
	}

	// 椅子が使えるかどうかをチェック
	chair_ids := []string{}
	for _, chair := range chairs {
		chair_ids = append(chair_ids, chair.ID)
	}
	empties := []checkEmpties{}
	query, args, err := sqlx.In(`SELECT chair_id, COUNT(*) = 0 AS is_empty FROM (SELECT chair_id, COUNT(chair_sent_at) = 6 AS completed FROM rides AS r LEFT JOIN ride_statuses AS rs ON rs.ride_id = r.id WHERE r.chair_id IN (?) GROUP BY ride_id, chair_id) is_completed WHERE completed = FALSE GROUP BY chair_id`, chair_ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := db.SelectContext(ctx, &empties, query, args...); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	emptyMap := map[string]bool{}
	for _, empty := range empties {
		emptyMap[empty.ChairID] = empty.Empty
	}

	availableChairs := []chairIdWithModel{}
	for _, chair := range chairs {
		empty, ok := emptyMap[chair.ID]
		if !ok || empty {
			availableChairs = append(availableChairs, chair)
		}
	}

	slog.Info("will match rides", slog.Int("rides", len(rides)), slog.Int("availableChairs", len(availableChairs)))
	// slog.Info("will match rides", slog.Int("rides", len(rides)), slog.Int("availableChairs", len(availableChairs)))
	if len(rides) > len(availableChairs) {
		rides = rides[:len(availableChairs)]
	}

	// ライドと椅子のマッチング
	weight_matrix := [][]float64{}
	zero_array := make([]float64, len(availableChairs)+1)
	weight_matrix = append(weight_matrix, zero_array)
	for _, ride := range rides {
		weight := []float64{}
		weight = append(weight, 0)

		for _, chair := range availableChairs {
			weight = append(weight, calcWeight(ride.PickupLatitude, ride.DestinationLatitude, chair.Speed))
		}
		weight_matrix = append(weight_matrix, weight)
	}

	_, P := hungarian(weight_matrix)

	chairIdAndRideId := []ChairIdAndRideId{}
	for i, p := range P {
		if p == 0 {
			continue
		}
		if i == 0 {
			continue
		}
		ride := rides[p-1]
		chair := availableChairs[i-1]
		chairIdAndRideId = append(chairIdAndRideId, ChairIdAndRideId{ChairID: chair.ID, RideID: ride.ID})
	}
	slog.Info("matched", slog.Int("matched", len(chairIdAndRideId)))

	if len(chairIdAndRideId) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	questionOfCount := ""
	for range chairIdAndRideId {
		questionOfCount += "?,"
	}
	questionOfCount = questionOfCount[:len(questionOfCount)-1]

	chairIds := []string{}
	rideIds := []string{}
	for _, id := range chairIdAndRideId {
		chairIds = append(chairIds, id.ChairID)
		rideIds = append(rideIds, id.RideID)
	}
	args = make([]any, len(rideIds)+len(chairIds)+len(rideIds))
	for i, v := range rideIds {
		args[i] = v
	}
	for i, v := range chairIds {
		args[len(rideIds)+i] = v
	}
	for i, v := range rideIds {
		args[len(rideIds)+len(chairIds)+i] = v
	}
	if _, err := db.ExecContext(ctx, `UPDATE rides SET chair_id = ELT(FIELD(id, `+questionOfCount+`), `+questionOfCount+`) WHERE id IN (`+questionOfCount+`)`, args...); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func calcWeight(now_distance int, destination_distance int, speed int) float64 {
	return float64(now_distance+destination_distance) / float64(speed)
}

func hungarian(A [][]float64) (float64, []int) {
	const infty = math.MaxFloat64
	N := len(A)
	M := len(A[0])
	P := make([]int, M)
	way := make([]int, M)
	U := make([]float64, N)
	V := make([]float64, M)
	minV := make([]float64, M)
	used := make([]bool, M)

	for i := range U {
		U[i] = 0
	}
	for i := range V {
		V[i] = 0
	}

	for i := 1; i < N; i++ {
		P[0] = i
		for j := range minV {
			minV[j] = infty
		}
		for j := range used {
			used[j] = false
		}
		j0 := 0
		for P[j0] != 0 {
			i0 := P[j0]
			j1 := 0
			used[j0] = true
			delta := infty
			for j := 1; j < M; j++ {
				if used[j] {
					continue
				}
				curr := A[i0][j] - U[i0] - V[j]
				if curr < minV[j] {
					minV[j] = curr
					way[j] = j0
				}
				if minV[j] < delta {
					delta = minV[j]
					j1 = j
				}
			}
			for j := 0; j < M; j++ {
				if used[j] {
					U[P[j]] += delta
					V[j] -= delta
				} else {
					minV[j] -= delta
				}
			}
			j0 = j1
		}
		for {
			P[j0] = P[way[j0]]
			j0 = way[j0]
			if j0 == 0 {
				break
			}
		}
	}

	return -V[0], P
}
