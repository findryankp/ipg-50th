const video = document.getElementById("video");
const canvas = document.getElementById("canvas");
const btnStart = document.getElementById("btnStart");
const btnStop = document.getElementById("btnStop");
const scanStatus = document.getElementById("scanStatus");
const resultBox = document.getElementById("result");
const syncBar = document.getElementById("syncBar");
const btnSync = document.getElementById("btnSync");

let stream = null;
let scanTimer = null;
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

async function startCamera() {
	try {
		stream = await navigator.mediaDevices.getUserMedia({
			video: { facingMode: "environment" },
		});
		video.srcObject = stream;
		btnStart.hidden = true;
		btnStop.hidden = false;
		scanStatus.textContent = "Mengarahkan kamera ke QR code...";
		scanning = true;
		scanTimer = setInterval(captureAndScan, 1200);
	} catch (err) {
		// Gagal (izin ditolak / tidak ada kamera) -- tampilkan tombol supaya
		// petugas bisa coba lagi setelah memperbaiki izin browser.
		btnStart.hidden = false;
		btnStop.hidden = true;
		scanStatus.textContent = "Gagal akses kamera: " + err.message;
	}
}

function stopCamera() {
	if (scanTimer) clearInterval(scanTimer);
	if (stream) stream.getTracks().forEach((t) => t.stop());
	video.srcObject = null;
	btnStart.hidden = false;
	btnStop.hidden = true;
	scanStatus.textContent = "";
}

let captureInFlight = false; // cegah foto baru ditembak sebelum foto sebelumnya selesai diproses server

async function captureAndScan() {
	if (!scanning || captureInFlight || video.readyState < 2) return;

	canvas.width = video.videoWidth;
	canvas.height = video.videoHeight;
	canvas.getContext("2d").drawImage(video, 0, 0);
	const dataUrl = canvas.toDataURL("image/jpeg", 0.85);

	captureInFlight = true;
	scanStatus.textContent = "Memindai...";
	try {
		const res = await fetch("/api/scan", {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ image: dataUrl }),
		});
		const data = await res.json();

		if (!data.status) {
			scanStatus.textContent = data.message || "QR belum terbaca";
			return;
		}

		scanning = false;
		scanStatus.textContent = "QR terbaca!";
		if (data.found) {
			showFound(data.peserta);
		} else {
			showNotFound(data.message);
		}
	} catch (err) {
		scanStatus.textContent = "Gagal terhubung ke server";
	} finally {
		captureInFlight = false;
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
}

btnStart.addEventListener("click", startCamera);
btnStop.addEventListener("click", stopCamera);

// Kamera langsung nyala begitu halaman dibuka -- petugas tidak perlu klik apa pun
// untuk mulai scan (browser cukup minta izin kamera sekali per perangkat).
scanStatus.textContent = "Meminta izin kamera...";
startCamera();
