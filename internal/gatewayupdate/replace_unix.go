//go:build !windows

package gatewayupdate

import "os"

func replacePath(source, destination string) error { return os.Rename(source, destination) }
