package io

import "os"

var (
	// devShmTempPath is set to '/dev/shm' if it exists, otherwise is "", which defaults to os.TempDir() when passed to os.CreateTemp()
	devShmTempPath string
)

func init() {
	fileInfo, err := os.Stat("/dev/shm")
	if err == nil && fileInfo.IsDir() {
		devShmTempPath = "/dev/shm"
	}
}

// TempPathUseDevShmIfAvailable will return '/dev/shm' if it is available on the system, otherwise it will return "", which defaults to os.TempDir() when passed to os.CreateTemp()
//
// The result of this function is used to store temporary files that are passed to kubectl code. These temporary files are usually cluster credentials or kubernetes manifests.
//
// NOTE: There are tradeoffs to using this function: '/dev/shm' is backed by RAM, and thus has limited size.
// - Since it is backed by RAM, this has the advantage of ensuring that sensitive data (such as credentials) are kept off disk (absent disk caching of memory)
// - However, due to the limited size, '/dev/shm' may run out of disk space, and/or is more vulnerable to slow leaks of files over time.
// You may instead consider using a disk-backed storage path like "", which os.CreateTemp() will default to e.g. '/tmp'.
func TempPathUseDevShmIfAvailable() string {
	return devShmTempPath
}

// DeleteFile is best effort deletion of a file
func DeleteFile(path string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return
	}
	_ = os.Remove(path)
}
