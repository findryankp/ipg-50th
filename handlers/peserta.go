package handlers

import (
	"database/sql"
	"net/http"
)

func ApiCheckin(db *sql.DB, cfg SyncConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pic, ok := currentPIC(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, scanResponse{Status: false, Message: "Belum login"})
			return
		}

		var body struct {
			NoPeserta string `json:"no_peserta"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, scanResponse{Status: false, Message: "Body tidak valid"})
			return
		}

		// "AND checkin_at IS NULL" membuat ini atomik di level SQLite: kalau dua
		// petugas scan QR yang sama nyaris bersamaan, hanya UPDATE yang commit
		// duluan yang benar-benar mengubah baris -- yang kedua RowsAffected=0,
		// jadi tidak dobel-tulis riwayat atau dobel-antre ke CI.
		res, err := db.Exec(`UPDATE peserta SET checkin_at = datetime('now', 'localtime'), checkin_by = ?
			WHERE no_peserta = ? AND checkin_at IS NULL`, pic, body.NoPeserta)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, scanResponse{Status: false, Message: "Gagal update database"})
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			existing, found, _ := findPeserta(db, body.NoPeserta)
			if !found {
				writeJSON(w, http.StatusOK, scanResponse{Status: false, Message: "Peserta tidak ditemukan"})
				return
			}
			writeJSON(w, http.StatusOK, scanResponse{
				Status:  true,
				Found:   true,
				Message: "Sudah check-in sebelumnya oleh " + existing.CheckinBy,
				Peserta: existing,
			})
			return
		}

		_, _ = db.Exec("INSERT INTO riwayat_scan (no_peserta, pic, aksi) VALUES (?, ?, ?)", body.NoPeserta, pic, "CHECKIN")

		// Checkin tidak butuh hasil lookup apa pun dari MSSQL, jadi cukup diantrikan
		// dan didorong oleh worker background -- tidak memblokir respons ke petugas.
		enqueueSync(db, "checkin", body.NoPeserta, map[string]string{
			"no_peserta": body.NoPeserta,
			"pic":        pic,
		})

		writeJSON(w, http.StatusOK, scanResponse{Status: true, Message: "Check-in berhasil"})
	}
}

func ApiRegistrasi(db *sql.DB, cfg SyncConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := currentPIC(r); !ok {
			writeJSON(w, http.StatusUnauthorized, scanResponse{Status: false, Message: "Belum login"})
			return
		}

		var body struct {
			NoPeserta string `json:"no_peserta"`
			Noreg     string `json:"noreg"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, scanResponse{Status: false, Message: "Body tidak valid"})
			return
		}
		if body.NoPeserta == "" || body.Noreg == "" {
			writeJSON(w, http.StatusOK, scanResponse{Status: false, Message: "No peserta dan noreg wajib diisi"})
			return
		}

		if cfg.MSSQLDB == nil {
			writeJSON(w, http.StatusServiceUnavailable, scanResponse{Status: false, Message: "Koneksi ke database MSSQL belum dikonfigurasi"})
			return
		}

		// Data pegawai (nama/divisi/dept/dst) bukan milik app ini -- diambil dengan
		// lookup noreg langsung ke MSSQL (KARYAWAN.dbo.DB_PEGAWAI), sama seperti
		// simpan_daftar() di CI, tapi dijalankan langsung dari Go ke database yang sama.
		peserta, err := registrasiInMSSQL(cfg.MSSQLDB, body.NoPeserta, body.Noreg)
		if err == nil {
			upsertPesertaSynced(db, peserta)
			_, _ = db.Exec("INSERT INTO riwayat_scan (no_peserta, pic, aksi) VALUES (?, ?, ?)", body.NoPeserta, picFromRequest(r), "REGISTRASI")
			writeJSON(w, http.StatusOK, scanResponse{Status: true, Found: true, Message: "Registrasi berhasil", Peserta: peserta})
			return
		}

		if _, rejected := err.(*syncRejected); rejected {
			writeJSON(w, http.StatusOK, scanResponse{Status: false, Message: err.Error()})
			return
		}

		// MSSQL tidak terjangkau saat ini: simpan noreg secara lokal dulu (status
		// pending) dan antrikan supaya worker background mencoba lagi begitu koneksi pulih.
		pending := &Peserta{NoPeserta: body.NoPeserta, Noreg: body.Noreg, Status: "pending"}
		if err := upsertPesertaPending(db, pending); err != nil {
			writeJSON(w, http.StatusInternalServerError, scanResponse{Status: false, Message: "Gagal simpan lokal"})
			return
		}
		enqueueSync(db, "registrasi", body.NoPeserta, map[string]string{
			"no_peserta": body.NoPeserta,
			"noreg":      body.Noreg,
		})
		_, _ = db.Exec("INSERT INTO riwayat_scan (no_peserta, pic, aksi) VALUES (?, ?, ?)", body.NoPeserta, picFromRequest(r), "REGISTRASI (pending)")

		writeJSON(w, http.StatusOK, scanResponse{
			Status:  true,
			Found:   true,
			Message: "Database MSSQL sedang tidak terjangkau. Noreg tersimpan di perangkat ini dan akan disinkronkan otomatis.",
			Peserta: pending,
		})
	}
}

func picFromRequest(r *http.Request) string {
	pic, _ := currentPIC(r)
	return pic
}

func upsertPesertaSynced(db *sql.DB, p *Peserta) {
	db.Exec(`INSERT INTO peserta (no_peserta, noreg, nama, divisi, dept, jobtitle, kelas, registrasi_status)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'synced')
		ON CONFLICT(no_peserta) DO UPDATE SET
			noreg = excluded.noreg, nama = excluded.nama, divisi = excluded.divisi,
			dept = excluded.dept, jobtitle = excluded.jobtitle, kelas = excluded.kelas,
			registrasi_status = 'synced'`,
		p.NoPeserta, p.Noreg, p.Nama, p.Divisi, p.Dept, p.Jobtitle, p.Kelas)
}

func upsertPesertaPending(db *sql.DB, p *Peserta) error {
	_, err := db.Exec(`INSERT INTO peserta (no_peserta, noreg, registrasi_status)
		VALUES (?, ?, 'pending')
		ON CONFLICT(no_peserta) DO UPDATE SET noreg = excluded.noreg, registrasi_status = 'pending'`,
		p.NoPeserta, p.Noreg)
	return err
}
