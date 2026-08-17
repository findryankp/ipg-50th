const btnStart = document.getElementById("btnStart");
const btnStop = document.getElementById("btnStop");
const cameraSelect = document.getElementById("cameraSelect");
const scanStatus = document.getElementById("scanStatus");
const resultBox = document.getElementById("result");
const syncBar = document.getElementById("syncBar");
const btnSync = document.getElementById("btnSync");

let scanning = true; // false saat sedang menampilkan hasil, supaya tidak tumpang tindih

// ---- Backup & antrian offline (localStorage) ----
// Setiap hasil check-in/registrasi disalin ke localStorage. Kalau request ke
// server gagal (koneksi putus), datanya masuk antrian "pending" dan otomatis
// dicoba kirim ulang saat online / saat tombol Sinkronkan ditekan, supaya
// data yang sudah discan tidak hilang begitu saja.
const BACKUP_KEY = "rg_backup";
const PENDING_KEY = "rg_pending";

function loadList(key) {
	try {
		return JSON.parse(localStorage.getItem(key)) || [];
	} catch {
		return [];
	}
}
function saveList(key, list) {
	localStorage.setItem(key, JSON.stringify(list));
}
function appendBackup(entry) {
	const list = loadList(BACKUP_KEY);
	list.push({ ...entry, waktu: new Date().toISOString() });
	saveList(BACKUP_KEY, list);
}
function queuePending(entry) {
	const list = loadList(PENDING_KEY);
	entry.id = Date.now() + "-" + Math.random().toString(36).slice(2, 8);
	list.push(entry);
	saveList(PENDING_KEY, list);
	appendBackup({ ...entry, synced: false });
	updateSyncBar();
	return entry.id;
}
function removePending(id) {
	saveList(PENDING_KEY, loadList(PENDING_KEY).filter((e) => e.id !== id));
	updateSyncBar();
}

function updateSyncBar() {
	if (!syncBar) return;
	const n = loadList(PENDING_KEY).length;
	if (n === 0) {
		syncBar.hidden = true;
		return;
	}
	syncBar.hidden = false;
	syncBar.querySelector("span").textContent = `${n} data belum tersinkron ke server`;
}

async function sendJSON(url, body) {
	const res = await fetch(url, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(body),
	});
	return res.json();
}

// Mencoba kirim satu item antrian. Mengembalikan true jika sudah bisa dianggap selesai
// (berhasil, atau gagal karena memang sudah pernah tersimpan sebelumnya).
async function trySyncItem(item) {
	try {
		if (item.type === "checkin") {
			await sendJSON("/api/checkin", { no_peserta: item.payload.no_peserta });
		} else if (item.type === "registrasi") {
			await sendJSON("/api/registrasi", item.payload);
		}
		// respons apapun (termasuk "sudah terdaftar") dianggap selesai — yang penting
		// request-nya sampai ke server, sisanya bukan masalah koneksi lagi.
		return true;
	} catch {
		return false; // masih belum ada koneksi, coba lagi nanti
	}
}

async function syncPending() {
	const list = loadList(PENDING_KEY);
	if (list.length === 0) return;
	if (btnSync) btnSync.disabled = true;

	for (const item of list) {
		const done = await trySyncItem(item);
		if (done) removePending(item.id);
	}

	if (btnSync) btnSync.disabled = false;
	updateSyncBar();
}

window.addEventListener("online", syncPending);
if (btnSync) btnSync.addEventListener("click", syncPending);
updateSyncBar();
syncPending();

// Decode QR dilakukan LANGSUNG DI BROWSER (bukan kirim foto ke server) --
// pendekatan yang sama dipakai CI (html5-qrcode), yang terbukti jauh lebih
// andal untuk kamera HP asli dibanding decode dari snapshot di server: frame
// diproses berkali-kali per detik dengan feedback langsung, bukan 1 foto tiap
// 1.2 detik yang gampang kena blur/fokus meleset.
const html5QrCode = new Html5Qrcode("reader");
let cameraStarted = false;
let selectedCameraId = null; // null = start pertama masih pakai facingMode "environment" (belum tahu daftar kamera)
let cameraListLoaded = false;

// Banyak HP modern punya beberapa lensa "belakang" (wide, ultra-wide, macro).
// facingMode:"environment" tanpa spesifikasi bisa memilih lensa yang salah
// (mis. ultra-wide yang fisheye/susah fokus dekat) -- itu penyebab umum QR
// "selalu gagal dibaca" padahal kamera menyala normal. Kasih petugas pilihan
// manual supaya bisa pindah ke lensa yang benar.
//
// Dipanggil SETELAH start pertama (yang masih pakai facingMode, karena daftar
// kamera belum tentu bisa dibaca sebelum izin diberikan). Begitu daftarnya
// didapat, kita restart sekali pakai deviceId eksplisit -- supaya kamera yang
// BENAR-BENAR JALAN dijamin sama persis dengan yang tampil terpilih di
// dropdown (bukan sekadar tebakan facingMode yang mungkin beda device).
async function loadCameraList() {
	if (cameraListLoaded) return;

	let cameras = [];
	try {
		cameras = await Html5Qrcode.getCameras();
	} catch {
		return; // gagal enumerasi (izin belum diberikan, browser tidak dukung, dst) -- tetap bisa scan pakai facingMode default
	}
	cameraListLoaded = true;

	if (!cameras || cameras.length === 0) return;

	if (cameras.length === 1) {
		cameraSelect.hidden = true;
		selectedCameraId = cameras[0].id;
	} else {
		cameraSelect.innerHTML = "";
		cameras.forEach((cam, i) => {
			const opt = document.createElement("option");
			opt.value = cam.id;
			opt.textContent = cam.label || `Kamera ${i + 1}`;
			cameraSelect.appendChild(opt);
		});
		cameraSelect.hidden = false;

		// Default: kamera dengan label mengandung "back"/rear kalau ada, kalau tidak
		// pakai heuristik umum -- kamera belakang utama biasanya urutan terakhir.
		const backCam = cameras.find((c) => /back|rear|belakang|environment/i.test(c.label));
		selectedCameraId = (backCam || cameras[cameras.length - 1]).id;
		cameraSelect.value = selectedCameraId;
	}

	// Restart pakai deviceId eksplisit supaya stream yang jalan dijamin cocok
	// dengan pilihan di dropdown, bukan hasil tebakan facingMode browser.
	if (cameraStarted) {
		await stopCamera();
		await startCamera();
	}
}

cameraSelect.addEventListener("change", async () => {
	selectedCameraId = cameraSelect.value;
	if (cameraStarted) {
		await stopCamera();
		await startCamera();
	}
});

async function startCamera() {
	if (cameraStarted) return;

	const config = {
		fps: 10,
		qrbox: { width: 250, height: 250 },
	};
	const cameraTarget = selectedCameraId ? { deviceId: { exact: selectedCameraId } } : { facingMode: "environment" };

	try {
		await html5QrCode.start(cameraTarget, config, onDecoded, onDecodeAttemptFailed);
		cameraStarted = true;
		btnStart.hidden = true;
		btnStop.hidden = false;
		scanStatus.textContent = "Mengarahkan kamera ke QR code...";
		scanning = true;
		loadCameraList(); // isi daftar kamera setelah izin didapat (no-op kalau sudah pernah)
	} catch (err) {
		btnStart.hidden = false;
		btnStop.hidden = true;
		scanStatus.textContent = "Gagal akses kamera: " + err;
	}
}

async function stopCamera() {
	if (!cameraStarted) return;
	try {
		await html5QrCode.stop();
	} catch {
		// abaikan -- kamera mungkin sudah berhenti sendiri
	}
	cameraStarted = false;
	btnStart.hidden = false;
	btnStop.hidden = true;
	scanStatus.textContent = "";
}

// Dipanggil html5-qrcode tiap frame yang GAGAL didecode -- normal (kamera belum
// pas ke QR), jangan diapa-apakan, cuma supaya callback wajib ini tidak error.
function onDecodeAttemptFailed() {}

async function onDecoded(decodedText) {
	if (!scanning) return; // lagi menampilkan hasil sebelumnya, abaikan dulu
	scanning = false;

	// Bekukan preview kamera (tanpa restart stream) selagi hasil ditampilkan --
	// lebih cepat pulih daripada stop()/start() ulang tiap ganti peserta.
	try {
		html5QrCode.pause(true);
	} catch {
		// beberapa browser melempar kalau dipause di momen yang salah, aman diabaikan
	}

	scanStatus.textContent = "Memproses...";
	try {
		const data = await sendJSON("/api/scan", { text: decodedText });

		if (!data.status) {
			scanStatus.textContent = data.message || "Gagal memproses QR";
			resumeScan();
			return;
		}

		scanStatus.textContent = "QR terbaca!";
		if (data.found) {
			showFound(data.peserta);
		} else {
			showNotFound(data.message);
		}
	} catch (err) {
		scanStatus.textContent = "Gagal terhubung ke server";
		resumeScan();
	}
}

function fillFields(root, obj) {
	root.querySelectorAll("[data-f]").forEach((el) => {
		const key = el.getAttribute("data-f");
		el.textContent = obj[key] || "-";
	});
}

function showFound(peserta) {
	const tpl = document.getElementById("tplFound").content.cloneNode(true);
	fillFields(tpl, peserta);

	if (peserta.registrasi_status === "pending") {
		tpl.querySelector("[data-pending]").hidden = false;
	}

	const btnCheckin = tpl.getElementById("btnCheckin");
	if (peserta.checkin_at) {
		// Sudah checkin sebelumnya (baik oleh diri sendiri maupun petugas lain) --
		// tidak perlu tunggu klik apa pun, langsung lanjut scan berikutnya.
		btnCheckin.textContent = "Sudah Check-in";
		btnCheckin.disabled = true;
		scheduleAutoResume();
	}
	btnCheckin.addEventListener("click", async () => {
		btnCheckin.disabled = true;
		try {
			const data = await sendJSON("/api/checkin", { no_peserta: peserta.no_peserta });
			appendBackup({ type: "checkin", payload: { no_peserta: peserta.no_peserta }, synced: true });
			btnCheckin.textContent = data.status ? "Sudah Check-in" : data.message;
			if (!data.status) {
				btnCheckin.disabled = false;
				return;
			}
		} catch {
			queuePending({ type: "checkin", payload: { no_peserta: peserta.no_peserta } });
			btnCheckin.textContent = "Tersimpan lokal, akan disinkronkan";
		}
		// Check-in selesai (sukses ataupun tersimpan offline) -- lanjut otomatis.
		scheduleAutoResume();
	});

	tpl.getElementById("btnScanLagi").addEventListener("click", resumeScan);

	resultBox.innerHTML = "";
	resultBox.appendChild(tpl);
	resultBox.hidden = false;
}

function showNotFound(kode) {
	const tpl = document.getElementById("tplNotFound").content.cloneNode(true);
	tpl.querySelector('[data-f="kode"]').textContent = kode;
	tpl.querySelector('input[name="no_peserta"]').value = kode;

	const form = tpl.getElementById("formRegistrasi");
	form.addEventListener("submit", async (e) => {
		e.preventDefault();
		const fd = new FormData(form);
		const body = Object.fromEntries(fd.entries());

		try {
			const data = await sendJSON("/api/registrasi", body);
			if (data.status) {
				appendBackup({ type: "registrasi", payload: body, synced: true });
				showFound(data.peserta);
			} else {
				alert(data.message);
			}
		} catch {
			queuePending({ type: "registrasi", payload: body });
			alert("Tidak ada koneksi ke server. Data disimpan di perangkat ini dan akan otomatis disinkronkan saat online.");
			showFound(body);
		}
	});

	tpl.getElementById("btnScanLagi2").addEventListener("click", resumeScan);

	resultBox.innerHTML = "";
	resultBox.appendChild(tpl);
	resultBox.hidden = false;
}

const AUTO_RESUME_MS = 2500; // jeda baca hasil sebelum kamera otomatis lanjut scan lagi
let autoResumeTimer = null;

function scheduleAutoResume() {
	clearTimeout(autoResumeTimer);
	scanStatus.textContent = "Lanjut scan otomatis...";
	autoResumeTimer = setTimeout(resumeScan, AUTO_RESUME_MS);
}

function resumeScan() {
	clearTimeout(autoResumeTimer);
	resultBox.hidden = true;
	resultBox.innerHTML = "";
	scanning = true;
	scanStatus.textContent = "Mengarahkan kamera ke QR code...";
	if (cameraStarted) {
		try {
			html5QrCode.resume();
		} catch {
			// kalau resume gagal (misal belum sempat pause), tidak fatal
		}
	}
}

btnStart.addEventListener("click", startCamera);
btnStop.addEventListener("click", stopCamera);

// Kamera langsung nyala begitu halaman dibuka -- petugas tidak perlu klik apa pun
// untuk mulai scan (browser cukup minta izin kamera sekali per perangkat).
scanStatus.textContent = "Meminta izin kamera...";
startCamera();
