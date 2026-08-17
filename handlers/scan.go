package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

type Peserta struct {
	NoPeserta string `json:"no_peserta"`
	Noreg     string `json:"noreg"`
	Nama      string `json:"nama"`
	Divisi    string `json:"divisi"`
	Dept      string `json:"dept"`
	Jobtitle  string `json:"jobtitle"`
	Kelas     string `json:"kelas"`
	CheckinAt string `json:"checkin_at"`
	CheckinBy string `json:"checkin_by"`
	Status    string `json:"registrasi_status,omitempty"` // 'pending' selama menunggu lookup pegawai dari CI
}

type scanResponse struct {
	Status  bool     `json:"status"`
	Message string   `json:"message"`
	Found   bool     `json:"found"`
	Peserta *Peserta `json:"peserta,omitempty"`
}

func ScanPage(w http.ResponseWriter, r *http.Request, pic string) {
	render(w, "scan.html", map[string]any{"PIC": pic})
}

func ApiScan(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pic, ok := currentPIC(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, scanResponse{Status: false, Message: "Belum login"})
			return
		}

		var body struct {
			Text string `json:"text"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, scanResponse{Status: false, Message: "Body tidak valid"})
			return
		}
		if body.Text == "" {
			writeJSON(w, http.StatusOK, scanResponse{Status: false, Message: "QR kosong"})
			return
		}

		noPeserta := lastN(body.Text, 8)

		p, found, err := findPeserta(db, noPeserta)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, scanResponse{Status: false, Message: "Gagal query database"})
			return
		}

		_, _ = db.Exec("INSERT INTO riwayat_scan (no_peserta, pic, aksi) VALUES (?, ?, ?)", noPeserta, pic, "SCAN")

		if !found {
			writeJSON(w, http.StatusOK, scanResponse{Status: true, Found: false, Message: noPeserta})
			return
		}
		writeJSON(w, http.StatusOK, scanResponse{Status: true, Found: true, Peserta: p})
	}
}

// lastN meniru "decodedText.slice(-8)" di scan_id.php milik CI: QR fisik yang
// dipakai acara ini meng-encode string yang lebih panjang (URL/kode), dan
// no_peserta sebenarnya adalah N karakter terakhirnya. Kalau teksnya lebih
// pendek dari n, dipakai apa adanya (sama seperti perilaku slice() di JS).
func lastN(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

func findPeserta(db *sql.DB, noPeserta string) (*Peserta, bool, error) {
	var p Peserta
	var noreg, nama, divisi, dept, jobtitle, kelas, checkinAt, checkinBy sql.NullString
	row := db.QueryRow(`SELECT no_peserta, noreg, nama, divisi, dept, jobtitle, kelas, checkin_at, checkin_by, registrasi_status
		FROM peserta WHERE no_peserta = ?`, noPeserta)
	err := row.Scan(&p.NoPeserta, &noreg, &nama, &divisi, &dept, &jobtitle, &kelas, &checkinAt, &checkinBy, &p.Status)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	p.Noreg, p.Nama, p.Divisi, p.Dept, p.Jobtitle, p.Kelas = noreg.String, nama.String, divisi.String, dept.String, jobtitle.String, kelas.String
	p.CheckinAt, p.CheckinBy = checkinAt.String, checkinBy.String
	return &p, true, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
