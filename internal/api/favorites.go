// Market favorites (spec #24, story 18): the member watchlist behind
// the market table's star column. The api owns the storage so the list
// follows the member across devices; the web keeps localStorage as the
// anonymous fallback and syncs the whole list — PUT replaces it, the
// front never merges server and local state half-way.
package api

import (
	"net/http"

	"github.com/jackc/pgx/v5"
)

// maxFavorites caps a watchlist: enough for any real star column, small
// enough that a runaway client cannot turn the table into junk.
const maxFavorites = 50

// RegisterFavorites mounts the watchlist routes on the PUBLIC mux.
func (a *memberAPI) RegisterFavorites(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/uc/favorites", a.withSession(a.handleFavoritesList))
	mux.HandleFunc("PUT /api/uc/favorites", a.withSession(a.handleFavoritesReplace))
}

func (a *memberAPI) handleFavoritesList(w http.ResponseWriter, r *http.Request, s Session) {
	rows, err := a.pool.Query(r.Context(),
		`SELECT market FROM market_favorites WHERE member_id = $1 ORDER BY created_at`, s.MemberID)
	if err != nil {
		writeInternalErr(w, http.StatusInternalServerError, "The watchlist could not be read.")
		return
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			writeInternalErr(w, http.StatusInternalServerError, "The watchlist could not be read.")
			return
		}
		names = append(names, name)
	}
	writeJSON(w, http.StatusOK, map[string]any{"markets": names})
}

func (a *memberAPI) handleFavoritesReplace(w http.ResponseWriter, r *http.Request, s Session) {
	var req struct {
		Markets []string `json:"markets"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	seen := make(map[string]bool, len(req.Markets))
	names := make([]string, 0, len(req.Markets))
	for _, name := range req.Markets {
		if name == "" || seen[name] {
			writeInternalErr(w, http.StatusBadRequest, "Every favorite needs a Market name.")
			return
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) > maxFavorites {
		writeInternalErr(w, http.StatusBadRequest, "A watchlist holds at most 50 Markets.")
		return
	}

	err := pgx.BeginFunc(r.Context(), a.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `DELETE FROM market_favorites WHERE member_id = $1`, s.MemberID); err != nil {
			return err
		}
		for _, name := range names {
			if _, err := tx.Exec(r.Context(),
				`INSERT INTO market_favorites (member_id, market) VALUES ($1, $2)`, s.MemberID, name); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		writeInternalErr(w, http.StatusInternalServerError, "The watchlist could not be saved.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"markets": names})
}
