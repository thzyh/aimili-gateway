package buildinfo

import "runtime"

const APIVersion = "v1"

var (
	Version = "dev"
	Commit  = "unknown"
	BuiltAt = "unknown"
)

type Info struct {
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	BuiltAt    string `json:"builtAt"`
	APIVersion string `json:"apiVersion"`
	Platform   string `json:"platform"`
}

func Current() Info {
	return Info{
		Version:    Version,
		Commit:     Commit,
		BuiltAt:    BuiltAt,
		APIVersion: APIVersion,
		Platform:   runtime.GOOS + "-" + runtime.GOARCH,
	}
}
