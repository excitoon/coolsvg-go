//go:build !windows

package main

import "os"

func replaceOutput(temporaryPath, destinationPath string) error {
	return os.Rename(temporaryPath, destinationPath)
}
