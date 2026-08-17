package handlers

import (
	"html/template"
	"log"
	"net/http"
)

var tmpl = template.Must(template.ParseGlob("templates/*.html"))

func render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// App ini sering di-update saat event berlangsung -- jangan sampai HP petugas
	// menyimpan versi HTML lama (khususnya browser mobile suka agresif nge-cache).
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "gagal render halaman", http.StatusInternalServerError)
	}
}
