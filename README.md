# registrasi-go

Aplikasi scan QR code untuk registrasi & check-in peserta. Backend Go (SQLite, pure-Go, tanpa CGO)
+ frontend HTML/CSS/JS biasa (tanpa framework/CDN) supaya ringan. QR code di-decode di server
(pakai gozxing) dari foto yang diambil browser via kamera.

Sistem utama tetap **CI** (PHP CodeIgniter di `../registrasi`). App ini adalah alat bantu scan yang
cepat & tahan putus koneksi: semua aksi ditulis dulu ke SQLite lokal (instan, tidak pernah menunggu
jaringan), lalu disinkronkan **satu arah** langsung ke **database MSSQL yang sama dipakai CI**
(`dbPeserta`, tabel `peserta_hut50`) — tanpa lewat CI/PHP sama sekali, jadi hanya ada **satu sumber
data** untuk dua sistem ini, tidak ada endpoint perantara yang perlu dipelihara dobel.

## Menjalankan

```
$env:MSSQL_HOST="192.168.2.1"
$env:MSSQL_PORT="1433"
$env:MSSQL_USER="<user yang sama dengan ../registrasi/application/config/database.php>"
$env:MSSQL_PASSWORD="<password yang sama>"
$env:MSSQL_DATABASE="dbPeserta"
go run .
```

Kalau variabel-variabel di atas tidak diset, sync ke MSSQL nonaktif (berguna untuk development
lokal) — semua data tetap tersimpan di SQLite dan menunggu di `sync_queue` sampai variabel ini diset
dan servernya dijalankan ulang. Kredensial **sengaja tidak di-hardcode** di source code — selalu
lewat env var.

Buka http://localhost:8082 — akan redirect ke halaman login.

Akun PIC awal (dibuat otomatis saat pertama kali dijalankan):

- Noreg: `admin`

Tambah PIC lain langsung lewat SQLite (`data/registrasi.db`), tabel `pic`.

## Alur

1. Login sebagai PIC (`/login`).
2. `/scan` — kamera otomatis nyala saat halaman dibuka, foto otomatis dikirim ke server tiap ~1.2
   detik dan di-decode di sisi Go (butuh koneksi ke server Go saat itu juga, karena decode-nya di server).
3. Jika kode sudah dikenal secara lokal → tampil data peserta + tombol **Check-in**.
4. Jika belum → petugas cukup input **noreg**. App ini query **langsung ke MSSQL**
   (`KARYAWAN.dbo.DB_PEGAWAI`, logika sama persis dengan `simpan_daftar()` yang dulu ada di CI) untuk
   ambil nama/divisi/dept/jabatan/kelas, lalu upsert ke `peserta_hut50`. Kalau MSSQL sedang tidak
   terjangkau, noreg tetap tersimpan lokal berstatus **pending** dan otomatis disinkronkan begitu
   koneksi pulih.
5. Check-in juga ditulis lokal dulu (instan, atomik — 2 petugas scan QR sama tidak dobel-tulis), lalu
   diantrikan ke `sync_queue` dan didorong oleh worker background setiap 5 detik ke `peserta_hut50`.
6. `/riwayat` — log semua aksi SCAN/CHECKIN/REGISTRASI di app ini.

Selain sync server-ke-server ini, browser juga menyimpan salinan hasil check-in/registrasi di
`localStorage` sebagai lapisan cadangan kalau koneksi *browser ke server Go* (bukan Go ke MSSQL) yang
putus — lihat bar "belum tersinkron" di halaman `/scan`.

## Sisi CI (PHP)

**Tidak ada perubahan apa pun** di `../registrasi` — CI membaca `peserta_hut50` seperti biasa dan
otomatis melihat hasil checkin/registrasi dari app ini karena keduanya menulis ke baris yang sama di
database yang sama. (Endpoint HTTP `Scan/api_registrasi`/`api_checkin` yang sempat ditambahkan di
percobaan sebelumnya sudah dihapus lagi karena sekarang tidak diperlukan.)

## Performa

- Tiap request ditangani goroutine sendiri (Go), jadi ratusan/ribuan koneksi bersamaan bukan masalah
  dari sisi server.
- SQLite dijalankan dalam mode **WAL** — pembacaan (lookup saat scan) tidak antre di belakang
  penulisan (checkin/registrasi/log), jadi lookup tetap cepat walau banyak petugas checkin bersamaan.
- Decode QR (CPU-bound) berjalan paralel per request memakai banyak core.
- Sync ke MSSQL tidak memblokir petugas — checkin selalu instan secara lokal, dan didorong ke MSSQL
  di background terlepas dari cepat/lambatnya jaringan ke server database.

## Struktur

- `main.go` — routing (`net/http` ServeMux bawaan Go 1.22+, tanpa router pihak ketiga) + buka koneksi MSSQL + start worker sync
- `db/` — koneksi SQLite (WAL) + migrasi schema otomatis
- `handlers/` — login/session, scan+decode QR, checkin, registrasi, riwayat, sync ke MSSQL (`sync.go`)
- `templates/` — halaman HTML (`html/template`, tanpa JS framework)
- `static/` — CSS & JS vanilla (capture kamera via `getUserMedia`, backup lokal di `localStorage`)
- `data/registrasi.db` — file database SQLite (dibuat otomatis)
