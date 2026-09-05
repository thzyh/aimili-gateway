//go:build windows

package updatetxn

import (
	"os"

	"golang.org/x/sys/windows"
)

func openedFileMetadata(file *os.File, _ os.FileInfo) (uint32, uint64, bool, error) {
	var metadata windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &metadata); err != nil {
		return 0, 0, false, err
	}
	return 0, uint64(metadata.NumberOfLinks), false, nil
}

func insecurePermissions(os.FileInfo) bool {
	return false
}
