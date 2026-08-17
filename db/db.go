package db

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS pic (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	noreg TEXT UNIQUE NOT NULL,
	nama TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS peserta (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	no_peserta TEXT UNIQUE NOT NULL,
	noreg TEXT,
	nama TEXT,
	divisi TEXT,
	dept TEXT,
	jobtitle TEXT,
	kelas TEXT,
	checkin_at TEXT,
	checkin_by TEXT,
	registrasi_status TEXT NOT NULL DEFAULT 'synced', -- 'pending' selama menunggu lookup pegawai dari CI
	created_at TEXT NOT NULL DEFAULT (datetime('now', 'localtime'))
);

CREATE TABLE IF NOT EXISTS riwayat_scan (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	no_peserta TEXT NOT NULL,
	pic TEXT NOT NULL,
	aksi TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now', 'localtime'))
);

-- Antrian satu-arah Go -> CI. Setiap checkin/registrasi ditulis dulu ke SQLite
-- lokal (cepat, tahan putus koneksi), lalu worker background mendorongnya ke
-- endpoint HTTP CI. status: pending -> synced (atau error, dicoba lagi).
CREATE TABLE IF NOT EXISTS sync_queue (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	jenis TEXT NOT NULL, -- 'checkin' | 'registrasi'
	no_peserta TEXT NOT NULL,
	payload TEXT NOT NULL, -- JSON
	status TEXT NOT NULL DEFAULT 'pending',
	attempts INTEGER NOT NULL DEFAULT 0,
	last_error TEXT,
	created_at TEXT NOT NULL DEFAULT (datetime('now', 'localtime')),
	synced_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_sync_queue_status ON sync_queue(status);
`

func Open(path string) *sql.DB {
	// Folder tujuan (misal "data/") tidak dilacak git kalau kosong, jadi bisa
	// saja tidak ada begitu repo di-copy/clone ke server lain -- buat dulu
	// supaya sqlite tidak gagal dengan "unable to open database file".
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("gagal membuat folder database %q: %v", dir, err)
		}
	}

	// WAL: pembacaan (lookup peserta saat scan) tidak perlu antre di belakang
	// penulisan (checkin/registrasi/log), jadi throughput scan bersamaan jauh
	// lebih baik dibanding mode default (rollback journal, 1 koneksi total).
	conn, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		log.Fatalf("gagal membuka database: %v", err)
	}
	conn.SetMaxOpenConns(8)

	if _, err := conn.Exec(schema); err != nil {
		log.Fatalf("gagal migrasi schema: %v", err)
	}

	seedPic(conn)

	return conn
}

// seedPic membuat akun PIC contoh supaya bisa langsung login saat pertama kali dijalankan.
func seedPic(conn *sql.DB) {
	var count int
	if err := conn.QueryRow("SELECT COUNT(*) FROM pic").Scan(&count); err != nil {
		log.Fatalf("gagal cek data pic: %v", err)
	}
	if count > 0 {
		return
	}
	_, err := conn.Exec("INSERT INTO pic (noreg, nama) VALUES (?, ?)", "admin", "Administrator")
	if err != nil {
		log.Fatalf("gagal seed data pic: %v", err)
	}
	log.Println("Akun PIC awal dibuat -> noreg: admin")
}
