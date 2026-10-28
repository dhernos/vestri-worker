package database

import (
	"bytes"
	"embed"
	"fmt"
	"regexp"
	"text/template"
)

//go:embed templates/*.yml
var composeTemplates embed.FS

type composeValues struct {
	Project      string
	Database     string
	Username     string
	Password     string
	RootPassword string
	Host         string
	Network      string
}

var safeComposeValue = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

func renderCompose(engine string, values composeValues) (string, error) {
	if engine != "mariadb" && engine != "postgres" {
		return "", fmt.Errorf("unsupported database engine")
	}
	for _, value := range []string{values.Project, values.Database, values.Username, values.Password, values.Host, values.Network} {
		if !safeComposeValue.MatchString(value) {
			return "", fmt.Errorf("invalid database compose value")
		}
	}
	if !safeComposeValue.MatchString(values.RootPassword) {
		return "", fmt.Errorf("invalid database administrator password")
	}
	parsed, err := template.ParseFS(composeTemplates, "templates/"+engine+".yml")
	if err != nil {
		return "", err
	}
	var output bytes.Buffer
	if err := parsed.Execute(&output, values); err != nil {
		return "", err
	}
	return output.String(), nil
}
