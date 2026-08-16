package handlers

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
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

// decodeQRFromDataURL menerima gambar dalam bentuk data URL (hasil canvas.toDataURL di browser)
// dan mengembalikan teks yang tertulis pada QR code, di-decode sepenuhnya di sisi server.
func decodeQRFromDataURL(dataURL string) (string, error) {
	idx := strings.Index(dataURL, ",")
	if idx == -1 {
		return "", errBadImage
	}
	raw, err := base64.StdEncoding.DecodeString(dataURL[idx+1:])
	if err != nil {
		return "", err
	}

	img, _, err := image.Decode(strings.NewReader(string(raw)))
	if err != nil {
		return "", err
	}

	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", err
	}

	result, err := qrcode.NewQRCodeReader().Decode(bmp, nil)
	if err != nil {
		return "", err
	}
	return result.GetText(), nil
}

var errBadImage = &qrError{"format gambar tidak valid"}

type qrError struct{ msg string }

func (e *qrError) Error() string { return e.msg }

func ApiScan(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pic, ok := currentPIC(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, scanResponse{Status: false, Message: "Belum login"})
			return
		}

		// Batasi ukuran foto (max ~8MB) supaya satu klien nakal/rusak tidak bisa
		// membanjiri memori server dengan payload raksasa.
		r.Body = http.MaxBytesReader(w, r.Body, 8<<20)

		var body struct {
			Image string `json:"image"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, scanResponse{Status: false, Message: "Body tidak valid"})
			return
		}

		noPeserta, err := decodeQRFromDataURL(body.Image)
		if err != nil {
			writeJSON(w, http.StatusOK, scanResponse{Status: false, Message: "QR code tidak terbaca, coba lagi"})
			return
		}

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
