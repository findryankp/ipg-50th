package handlers

import (
	"database/sql"
	"net/http"
)

func LoginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := currentPIC(r); ok {
		http.Redirect(w, r, "/scan", http.StatusSeeOther)
		return
	}
	render(w, "login.html", map[string]any{"Error": r.URL.Query().Get("error")})
}

func LoginSubmit(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		noreg := r.FormValue("noreg")

		var nama string
		err := db.QueryRow("SELECT nama FROM pic WHERE noreg = ?", noreg).Scan(&nama)
		if err != nil {
			http.Redirect(w, r, "/login?error=Noreg+tidak+ditemukan", http.StatusSeeOther)
			return
		}

		createSession(w, nama)
		http.Redirect(w, r, "/scan", http.StatusSeeOther)
	}
}

func Logout(w http.ResponseWriter, r *http.Request) {
	destroySession(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
