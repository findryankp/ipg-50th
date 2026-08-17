package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/microsoft/go-mssqldb"

	"registrasi-go/db"
	"registrasi-go/handlers"
)

func main() {
	loadDotEnv(".env")

	conn := db.Open("data/registrasi.db")
	defer conn.Close()

	syncCfg := handlers.SyncConfig{MSSQLDB: openMSSQL()}

	handlers.StartSyncWorker(conn, syncCfg, 5*time.Second)

	mux := http.NewServeMux()

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	mux.HandleFunc("GET /login", handlers.LoginPage)
	mux.HandleFunc("POST /login", handlers.LoginSubmit(conn))
	mux.HandleFunc("GET /logout", handlers.Logout)

	mux.HandleFunc("GET /scan", handlers.RequireLoginPage(handlers.ScanPage))
	mux.HandleFunc("GET /riwayat", handlers.RequireLoginPage(handlers.RiwayatPage(conn)))

	mux.HandleFunc("POST /api/scan", handlers.ApiScan(conn))
	mux.HandleFunc("POST /api/checkin", handlers.ApiCheckin(conn, syncCfg))
	mux.HandleFunc("POST /api/registrasi", handlers.ApiRegistrasi(conn, syncCfg))

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/scan", http.StatusSeeOther)
	})

	// Timeout eksplisit supaya satu klien yang lambat/macet (koneksi jelek di venue)
	// tidak menahan resource server dan mengganggu petugas lain.
	srv := &http.Server{
		Addr:         ":8083",
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("Server jalan di http://localhost%s", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}

// openMSSQL membuka koneksi ke database MSSQL yang sama dipakai CI (dbPeserta),
// supaya checkin/registrasi dari app ini langsung sinkron ke satu sumber data
// yang sama -- tidak ada lagi endpoint HTTP perantara di CI. Kredensial TIDAK
// di-hardcode di sini (sengaja) -- wajib diisi lewat env var, samakan dengan
// application/config/database.php milik CI.
func openMSSQL() *sql.DB {
	host := os.Getenv("MSSQL_HOST")
	port := getenvDefault("MSSQL_PORT", "1433")
	user := os.Getenv("MSSQL_USER")
	pass := os.Getenv("MSSQL_PASSWORD")
	dbname := os.Getenv("MSSQL_DATABASE")

	if host == "" || user == "" || pass == "" || dbname == "" {
		log.Println("MSSQL: MSSQL_HOST/MSSQL_USER/MSSQL_PASSWORD/MSSQL_DATABASE belum diset -- sync ke MSSQL nonaktif, checkin/registrasi diantre lokal saja")
		return nil
	}

	q := url.Values{}
	q.Add("database", dbname)
	// Server MSSQL ini cuma bisa TLS 1.0, yang ditolak Go (minimum TLS-nya lebih
	// tinggi) -- CI sendiri juga sudah nonaktifkan enkripsi ke server yang sama
	// ('encrypt' => FALSE di database.php), jadi disamakan di sini.
	q.Add("encrypt", "disable")
	dsn := (&url.URL{
		Scheme:   "sqlserver",
		User:     url.UserPassword(user, pass),
		Host:     fmt.Sprintf("%s:%s", host, port),
		RawQuery: q.Encode(),
	}).String()

	conn, err := sql.Open("sqlserver", dsn)
	if err != nil {
		log.Printf("MSSQL: gagal siapkan koneksi (%v) -- checkin/registrasi akan diantre lokal sampai ini beres", err)
		return nil
	}
	conn.SetMaxOpenConns(5)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.PingContext(ctx); err != nil {
		log.Printf("MSSQL: belum bisa dijangkau saat start (%v) -- akan dicoba lagi otomatis oleh worker sync", err)
	} else {
		log.Printf("MSSQL: terkoneksi ke %s:%s/%s", host, port, dbname)
	}
	return conn
}

func getenvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// loadDotEnv membaca file .env sederhana (KEY=VALUE per baris, "#" untuk
// komentar) dan mengisi environment proses ini. Variabel yang sudah di-set
// lewat env asli (misal oleh systemd/docker) tidak ditimpa. File opsional --
// kalau tidak ada, ini no-op senyap (biar tetap bisa diatur lewat env var
// biasa di produksi).
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
}
