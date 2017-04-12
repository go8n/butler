package main

import (
	"time"

	humanize "github.com/dustin/go-humanize"
	"github.com/itchio/butler/comm"
	"github.com/itchio/wharf/archiver"
)

func unzip(file string, dir string, resumeFile string) {
	comm.Opf("Extracting zip %s to %s", file, dir)

	var zipUncompressedSize int64

	settings := archiver.ExtractSettings{
		Consumer:   comm.NewStateConsumer(),
		ResumeFrom: resumeFile,
		OnUncompressedSizeKnown: func(uncompressedSize int64) {
			zipUncompressedSize = uncompressedSize
			comm.StartProgressWithTotalBytes(uncompressedSize)
		},
	}

	startTime := time.Now()

	res, err := archiver.ExtractPath(file, dir, settings)
	comm.EndProgress()

	duration := time.Since(startTime)
	bytesPerSec := float64(zipUncompressedSize) / duration.Seconds()

	must(err)
	comm.Logf("Extracted %d dirs, %d files, %d symlinks, %s at %s/s", res.Dirs, res.Files, res.Symlinks,
		humanize.IBytes(uint64(zipUncompressedSize)), humanize.IBytes(uint64(bytesPerSec)))
}
