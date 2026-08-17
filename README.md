# registrasi-go

Aplikasi scan QR code untuk registrasi & check-in peserta. Backend Go (SQLite, pure-Go, tanpa CGO)
+ frontend HTML/CSS/JS biasa (tanpa framework/CDN) supaya ringan. QR code di-decode **langsung di
browser** pakai `html5-qrcode` (di-vendor lokal, sama library yang dipakai CI di `scan_id.php` —
disamakan setelah decode di server terbukti kurang andal untuk kamera HP asli dibanding decode
kontinu langsung dari live video seperti yang CI lakukan). Browser cuma kirim **teks hasil decode**
ke server, bukan foto.

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
2. `/scan` — kamera otomatis nyala saat halaman dibuka, `html5-qrcode` decode QR langsung dari live
   video di browser (~10fps, tanpa perlu foto/round-trip jaringan untuk baca QR-nya). Setelah teks
   ke-decode, cuma teks itu (bukan gambar) yang dikirim ke `/api/scan`. 8 karakter terakhir dari teks
   dipakai sebagai `no_peserta` — meniru persis `decodedText.slice(-8)` di `scan_id.php` milik CI,
   karena QR fisik acara ini meng-encode string lebih panjang (URL/kode) dan bagian akhirnya yang
   jadi ID sebenarnya.
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
- Decode QR tidak lagi membebani server sama sekali (dipindah ke browser) — server cuma terima teks
  pendek hasil decode, jauh lebih ringan dibanding proses+kirim foto tiap 1.2 detik.
- Sync ke MSSQL tidak memblokir petugas — checkin selalu instan secara lokal, dan didorong ke MSSQL
  di background terlepas dari cepat/lambatnya jaringan ke server database.

## Struktur

- `main.go` — routing (`net/http` ServeMux bawaan Go 1.22+, tanpa router pihak ketiga) + `.env`
  loader + buka koneksi MSSQL + start worker sync
- `db/` — koneksi SQLite (WAL) + migrasi schema otomatis
- `handlers/` — login/session, scan (terima teks hasil decode), checkin, registrasi, riwayat, sync ke MSSQL (`sync.go`)
- `templates/` — halaman HTML (`html/template`, tanpa JS framework)
- `static/js/vendor/html5-qrcode.min.js` — library decode QR client-side (di-vendor lokal, sama versi dipakai CI)
- `static/` — CSS & JS vanilla (kamera lewat `html5-qrcode`, backup lokal di `localStorage`)
- `data/registrasi.db` — file database SQLite (dibuat otomatis)
