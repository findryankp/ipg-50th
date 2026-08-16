package handlers

import (
	"database/sql"
	"errors"
	"log"
	"time"
)

// Config koneksi ke MSSQL yang sama dipakai CI (dbPeserta). MSSQLDB nil = sync
// nonaktif (berguna untuk development tanpa akses ke server database).
type SyncConfig struct {
	MSSQLDB *sql.DB
}

type syncQueueItem struct {
	ID        int64
	Jenis     string
	NoPeserta string
	Payload   string
}

// StartSyncWorker menjalankan loop background yang mendorong isi sync_queue
// (checkin) ke MSSQL setiap beberapa detik. Item yang gagal (MSSQL tidak
// terjangkau) tetap 'pending' dan dicoba lagi di putaran berikutnya -- jadi
// checkin tidak pernah hilang hanya karena koneksi ke database sedang putus.
func StartSyncWorker(db *sql.DB, cfg SyncConfig, interval time.Duration) {
	if cfg.MSSQLDB == nil {
		log.Println("Sync ke MSSQL nonaktif (belum dikonfigurasi)")
		return
	}
	go func() {
		for {
			runSyncOnce(db, cfg)
			time.Sleep(interval)
		}
	}()
}

func runSyncOnce(db *sql.DB, cfg SyncConfig) {
	rows, err := db.Query(`SELECT id, jenis, no_peserta, payload FROM sync_queue
		WHERE status = 'pending' ORDER BY id ASC LIMIT 50`)
	if err != nil {
		log.Printf("sync: gagal ambil antrian: %v", err)
		return
	}
	var items []syncQueueItem
	for rows.Next() {
		var it syncQueueItem
		if err := rows.Scan(&it.ID, &it.Jenis, &it.NoPeserta, &it.Payload); err == nil {
			items = append(items, it)
		}
	}
	rows.Close()

	for _, it := range items {
		err := pushToMSSQL(cfg.MSSQLDB, it)
		if err == nil {
			db.Exec(`UPDATE sync_queue SET status = 'synced', synced_at = datetime('now','localtime') WHERE id = ?`, it.ID)
			continue
		}

		if _, rejected := err.(*syncRejected); rejected {
			// Ditolak karena data (bukan soal koneksi) -- hentikan, jangan diulang selamanya.
			db.Exec(`UPDATE sync_queue SET status = 'error', attempts = attempts + 1, last_error = ? WHERE id = ?`, err.Error(), it.ID)
			continue
		}
		db.Exec(`UPDATE sync_queue SET attempts = attempts + 1, last_error = ? WHERE id = ?`, err.Error(), it.ID)
	}
}

func pushToMSSQL(mssql *sql.DB, it syncQueueItem) error {
	switch it.Jenis {
	case "checkin":
		var payload struct {
			NoPeserta string `json:"no_peserta"`
			Pic       string `json:"pic"`
		}
		if err := unmarshalPayload(it.Payload, &payload); err != nil {
			return err
		}
		return checkinInMSSQL(mssql, payload.NoPeserta, payload.Pic)
	case "registrasi":
		var payload struct {
			NoPeserta string `json:"no_peserta"`
			Noreg     string `json:"noreg"`
		}
		if err := unmarshalPayload(it.Payload, &payload); err != nil {
			return err
		}
		_, err := registrasiInMSSQL(mssql, payload.NoPeserta, payload.Noreg)
		return err
	default:
		return nil
	}
}

type syncRejected struct{ msg string }

func (e *syncRejected) Error() string { return e.msg }

// registrasiInMSSQL meniru persis logika simpan_daftar() yang sudah ada di CI
// (Scan.php): lookup pegawai dari KARYAWAN.dbo.DB_PEGAWAI berdasarkan noreg,
// pastikan noreg belum dipakai gelang lain, lalu upsert ke peserta_hut50 --
// dijalankan langsung dari Go ke database yang sama dipakai CI, tanpa lewat CI.
func registrasiInMSSQL(mssql *sql.DB, noPeserta, noreg string) (*Peserta, error) {
	var sbu, nama, divisi, dept, jobtitle, kelas sql.NullString
	row := mssql.QueryRow(`SELECT TOP 1 sbu, nama, divisi, dept, jobtitle, class
		FROM KARYAWAN.dbo.DB_PEGAWAI WHERE noreg = ? AND tgl_resign IS NULL`, noreg)
	if err := row.Scan(&sbu, &nama, &divisi, &dept, &jobtitle, &kelas); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &syncRejected{"GAGAL! Data pegawai tidak ditemukan !"}
		}
		return nil, err // error koneksi/DB -- boleh dicoba lagi nanti
	}

	var dipakai string
	err := mssql.QueryRow(`SELECT TOP 1 noreg FROM peserta_hut50
		WHERE noreg = ? AND no_peserta != ?`, noreg, noPeserta).Scan(&dipakai)
	if err == nil {
		return nil, &syncRejected{"GAGAL! Noreg sudah digunakan di gelang lain !"}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	var adaBaris string
	err = mssql.QueryRow(`SELECT TOP 1 no_peserta FROM peserta_hut50 WHERE no_peserta = ?`, noPeserta).Scan(&adaBaris)
	switch {
	case err == nil:
		_, err = mssql.Exec(`UPDATE peserta_hut50 SET noreg=?, sbu=?, nama=?, divisi=?, dept=?, jobtitle=?, class=?
			WHERE no_peserta = ?`, noreg, sbu, nama, divisi, dept, jobtitle, kelas, noPeserta)
	case errors.Is(err, sql.ErrNoRows):
		_, err = mssql.Exec(`INSERT INTO peserta_hut50 (no_peserta, noreg, sbu, nama, divisi, dept, jobtitle, class)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, noPeserta, noreg, sbu, nama, divisi, dept, jobtitle, kelas)
	}
	if err != nil {
		return nil, err
	}

	return &Peserta{
		NoPeserta: noPeserta,
		Noreg:     noreg,
		Nama:      nama.String,
		Divisi:    divisi.String,
		Dept:      dept.String,
		Jobtitle:  jobtitle.String,
		Kelas:     kelas.String,
	}, nil
}

func checkinInMSSQL(mssql *sql.DB, noPeserta, pic string) error {
	_, err := mssql.Exec(`UPDATE peserta_hut50 SET checkin = GETDATE(), pic = ? WHERE no_peserta = ?`, pic, noPeserta)
	return err
}

func enqueueSync(db *sql.DB, jenis, noPeserta string, payload any) {
	b, err := marshalPayload(payload)
	if err != nil {
		log.Printf("sync: gagal antrikan %s untuk %s: %v", jenis, noPeserta, err)
		return
	}
	_, err = db.Exec(`INSERT INTO sync_queue (jenis, no_peserta, payload) VALUES (?, ?, ?)`, jenis, noPeserta, b)
	if err != nil {
		log.Printf("sync: gagal antrikan %s untuk %s: %v", jenis, noPeserta, err)
	}
}
