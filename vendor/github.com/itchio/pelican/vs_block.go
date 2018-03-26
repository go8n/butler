package pelican

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"

	"github.com/itchio/wharf/state"
	"github.com/pkg/errors"
)

// PE version block utilities

type ReadSeekerAt interface {
	io.ReadSeeker
	io.ReaderAt
}

type VsBlock struct {
	Length      uint16
	ValueLength uint16
	Type        uint16
	Key         []byte
	EndOffset   int64

	ReadSeekerAt
}

func (vb *VsBlock) KeyString() string {
	return DecodeUTF16(vb.Key)
}

type VsFixedFileInfo struct {
	DwSignature        uint32
	DwStrucVersion     uint32
	DwFileVersionMS    uint32
	DwFileVersionLS    uint32
	DwProductVersionMS uint32
	DwProductVersionLS uint32
	DwFileFlagsMask    uint32
	DwFileFlags        uint32
	DwFileOS           uint32
	DwFileType         uint32
	DwFileSubtype      uint32
	DwFileDateMS       uint32
	DwFileDateLS       uint32
}

func parseVersion(info *PeInfo, consumer *state.Consumer, rawData []byte) error {
	br := bytes.NewReader(rawData)
	buf := make([]byte, 2)

	skipPadding := func(r ReadSeekerAt) error {
		for {
			_, err := r.Read(buf)
			if err != nil {
				if err == io.EOF {
					// alles gut
					return nil
				}
				return errors.WithStack(err)
			}

			if buf[0] != 0 || buf[1] != 0 {
				_, err = r.Seek(-2, io.SeekCurrent)
				if err != nil {
					return errors.WithStack(err)
				}
				break
			}
		}
		return nil
	}

	parseNullTerminatedString := func(r ReadSeekerAt) ([]byte, error) {
		var res []byte

		for {
			_, err := r.Read(buf)
			if err != nil {
				if errors.Cause(err) == io.EOF {
					return res, nil
				}
				return nil, errors.WithStack(err)
			}

			if buf[0] == 0 && buf[1] == 0 {
				break
			} else {
				res = append(res, buf...)
			}
		}
		return res, nil
	}

	parseVSBlock := func(r ReadSeekerAt) (*VsBlock, error) {
		startOffset, err := r.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, errors.WithStack(err)
		}

		var wLength uint16
		err = binary.Read(r, binary.LittleEndian, &wLength)
		if err != nil {
			return nil, errors.WithStack(err)
		}

		endOffset := startOffset + int64(wLength)
		sr := io.NewSectionReader(r, startOffset+2, int64(wLength)-2 /* we already read the wLength uint16 */)

		var wValueLength uint16
		err = binary.Read(sr, binary.LittleEndian, &wValueLength)
		if err != nil {
			return nil, errors.WithStack(err)
		}

		var wType uint16
		err = binary.Read(sr, binary.LittleEndian, &wType)
		if err != nil {
			return nil, errors.WithStack(err)
		}

		szKey, err := parseNullTerminatedString(sr)
		if err != nil {
			return nil, errors.WithStack(err)
		}

		err = skipPadding(sr)
		if err != nil {
			return nil, errors.WithStack(err)
		}

		return &VsBlock{
			Length:       wLength,
			ValueLength:  wValueLength,
			Type:         wType,
			Key:          szKey,
			EndOffset:    endOffset,
			ReadSeekerAt: sr,
		}, nil
	}

	vsVersionInfo, err := parseVSBlock(br)
	if err != nil {
		return errors.WithStack(err)
	}

	if vsVersionInfo.ValueLength == 0 {
		return nil
	}

	ffi := new(VsFixedFileInfo)
	err = binary.Read(vsVersionInfo, binary.LittleEndian, ffi)
	if err != nil {
		return errors.WithStack(err)
	}

	if ffi.DwSignature != 0xFEEF04BD {
		consumer.Debugf("invalid signature, either the version block is invalid or we messed up")
		return nil
	}

	err = skipPadding(vsVersionInfo)
	if err != nil {
		return errors.WithStack(err)
	}

	for {
		fileInfo, err := parseVSBlock(vsVersionInfo)
		if err != nil {
			if errors.Cause(err) == io.EOF {
				break
			}
			return errors.WithStack(err)
		}

		switch fileInfo.KeyString() {
		case "StringFileInfo":
			for {
				stable, err := parseVSBlock(fileInfo)
				if err != nil {
					if errors.Cause(err) == io.EOF {
						break
					}
					return errors.WithStack(err)
				}

				if isLanguageWhitelisted(stable.KeyString()) {
					for {
						str, err := parseVSBlock(stable)
						if err != nil {
							if errors.Cause(err) == io.EOF {
								break
							}
							return errors.WithStack(err)
						}

						keyString := str.KeyString()

						val, err := parseNullTerminatedString(str)
						if err != nil {
							return errors.WithStack(err)
						}
						valString := strings.TrimSpace(DecodeUTF16(val))

						consumer.Debugf("%s: %s", keyString, valString)
						info.VersionProperties[keyString] = valString
						_, err = stable.Seek(str.EndOffset, io.SeekStart)
						if err != nil {
							return errors.WithStack(err)
						}

						err = skipPadding(stable)
						if err != nil {
							return errors.WithStack(err)
						}
					}
				}

				_, err = fileInfo.Seek(stable.EndOffset, io.SeekStart)
				if err != nil {
					return errors.WithStack(err)
				}
			}
		case "VarFileInfo":
			// skip
		}

		_, err = vsVersionInfo.Seek(fileInfo.EndOffset, io.SeekStart)
		if err != nil {
			return errors.WithStack(err)
		}
	}

	return nil
}
