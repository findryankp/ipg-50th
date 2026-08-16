package handlers

import (
	"database/sql"
	"net/http"
)

type riwayatRow struct {
	NoPeserta string
	Nama      string
	PIC       string
	Aksi      string
	Waktu     string
}

func RiwayatPage(db *sql.DB) func(w http.ResponseWriter, r *http.Request, pic string) {
	return func(w http.ResponseWriter, r *http.Request, pic string) {
		rows, err := db.Query(`SELECT rs.no_peserta, COALESCE(p.nama, ''), rs.pic, rs.aksi, rs.created_at
			FROM riwayat_scan rs
			LEFT JOIN peserta p ON p.no_peserta = rs.no_peserta
			ORDER BY rs.id DESC
			LIMIT 200`)
		if err != nil {
			http.Error(w, "gagal ambil riwayat", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var list []riwayatRow
		for rows.Next() {
			var row riwayatRow
			if err := rows.Scan(&row.NoPeserta, &row.Nama, &row.PIC, &row.Aksi, &row.Waktu); err != nil {
				continue
			}
			list = append(list, row)
		}

		render(w, "riwayat.html", map[string]any{"PIC": pic, "Riwayat": list})
	}
}
