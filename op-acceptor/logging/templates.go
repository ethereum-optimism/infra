package logging

import (
	"embed"
	"html/template"
)

//go:embed templates/*.tmpl.html templates/static/*
var templateFS embed.FS

// GetHTMLTemplate returns the HTML template for the specified name
func GetHTMLTemplate(name string) (*template.Template, error) {
	return template.ParseFS(templateFS, "templates/"+name)
}

// GetStaticFile returns the content of a static file from the embedded filesystem
func GetStaticFile(filename string) ([]byte, error) {
	return templateFS.ReadFile("templates/static/" + filename)
}
