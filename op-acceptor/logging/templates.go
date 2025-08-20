package logging

import (
	"embed"
)

//go:embed templates/*.tmpl.html templates/static/*
var templateFS embed.FS

// GetStaticFile returns the content of a static file from the embedded filesystem
func GetStaticFile(filename string) ([]byte, error) {
	return templateFS.ReadFile("templates/static/" + filename)
}
