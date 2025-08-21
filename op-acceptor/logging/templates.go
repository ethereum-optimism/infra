package logging

import (
	"embed"
	"html/template"

	"github.com/ethereum-optimism/infra/op-acceptor/templates"
)

//go:embed templates/*.tmpl.html templates/static/*
var templateFS embed.FS

// GetHTMLTemplate returns the HTML template for the specified name with required functions
func GetHTMLTemplate(name string) (*template.Template, error) {
	tmpl := template.New(name).Funcs(templates.GetTemplateFunc())
	return tmpl.ParseFS(templateFS, "templates/"+name)
}

// GetRawTemplateContent returns the raw template content as a string
func GetRawTemplateContent(name string) (string, error) {
	content, err := templateFS.ReadFile("templates/" + name)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// GetStaticFile returns the content of a static file from the embedded filesystem
func GetStaticFile(filename string) ([]byte, error) {
	return templateFS.ReadFile("templates/static/" + filename)
}
