package logging

import (
	"embed"
	"html/template"
)

//go:embed templates/*.tmpl.html
var templateFS embed.FS

// GetHTMLTemplate returns the HTML template for the specified name
func GetHTMLTemplate(name string) (*template.Template, error) {
	return template.ParseFS(templateFS, "templates/"+name)
}
