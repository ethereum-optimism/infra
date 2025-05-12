package logging

import (
	"embed"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"runtime"
)

//go:embed templates/*.html.tmpl
var templateFS embed.FS

// GetHTMLTemplate returns the HTML template for the specified name
func GetHTMLTemplate(name string) (*template.Template, error) {
	// First try the embedded filesystem
	tmpl, err := template.ParseFS(templateFS, "templates/"+name)
	if err == nil {
		return tmpl, nil
	}

	// If embedded template fails, try to load from disk
	// Get the path to the current file
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return nil, fmt.Errorf("unable to determine current file path")
	}

	// Construct template path relative to this file
	templatePath := filepath.Join(filepath.Dir(filename), "templates", name)

	// Check if template file exists
	if _, err := os.Stat(templatePath); err != nil {
		return nil, fmt.Errorf("template not found at %s: %w", templatePath, err)
	}

	// Parse template from disk
	return template.ParseFiles(templatePath)
}
