package main

import (
	"database/sql"
	"errors"
	"math"
	"net/http"
)

// このAPIをインスタンス内から一定間隔で叩かせることで、椅子とライドをマッチングさせる
func internalGetMatching(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// MEMO: 一旦最も待たせているリクエストに適当な空いている椅子マッチさせる実装とする。おそらくもっといい方法があるはず…
	rides := []Ride{}
	if err := db.SelectContext(ctx, &rides, `SELECT * FROM rides WHERE chair_id IS NULL ORDER BY created_at LIMIT 100`); err != nil {
		writeError(w, http.StatusInternalServerError, err)
	}

	chairs := []Chair{}
	if err := db.SelectContext(ctx, &chairs, `SELECT * FROM chairs WHERE is_active = TRUE LIMIT 200`); err != nil {
		writeError(w, http.StatusInternalServerError, err)
	}

	// 椅子が使えるかどうかをチェック
	

	matched := &Chair{}
	empty := false
	for i := 0; i < 10; i++ {
		if err := db.GetContext(ctx, matched, "SELECT * FROM chairs INNER JOIN (SELECT id FROM chairs WHERE is_active = TRUE ORDER BY RAND() LIMIT 1) AS tmp ON chairs.id = tmp.id LIMIT 1"); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			writeError(w, http.StatusInternalServerError, err)
		}

		if err := db.GetContext(ctx, &empty, "SELECT COUNT(*) = 0 FROM (SELECT COUNT(chair_sent_at) = 6 AS completed FROM ride_statuses WHERE ride_id IN (SELECT id FROM rides WHERE chair_id = ?) GROUP BY ride_id) is_completed WHERE completed = FALSE", matched.ID); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if empty {
			break
		}
	}
	if !empty {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if _, err := db.ExecContext(ctx, "UPDATE rides SET chair_id = ? WHERE id = ?", matched.ID, ride.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
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
