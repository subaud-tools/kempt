package handlers

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	tools "github.com/subaud-tools/tools-common"
)

// This file holds the verified-atomic install primitive shared by the
// github-release and download handlers. Both fetch an asset + a checksum
// document, verify sha256 FAIL-CLOSED, extract the target binary (tar.gz or
// raw), and land it atomically at a destination directory.

// checksumHex finds the sha256 hex for assetName inside a checksum document.
// It accepts shasum format lines ("<hex>  <name>", two-space, but a single
// space is tolerated), an optional "./" prefix on the filename, and a
// bare-hex file (a single whitespace-free token, used by ".sha256" sidecars).
// The returned hex is lowercase-normalized.
func checksumHex(sumsFile []byte, assetName string) (string, error) {
	content := string(sumsFile)
	// Bare-hex fallback: the whole file is a single token.
	if trimmed := strings.TrimSpace(content); trimmed != "" && !strings.ContainsAny(trimmed, " \t\r\n") {
		return strings.ToLower(trimmed), nil
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "./")
		if name == assetName {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum line for asset %s", assetName)
}

// extractBinary returns the binary bytes. When assetName ends in .tar.gz
// (or .tgz) it decompresses and finds the tar entry whose base name equals
// bin; otherwise the asset bytes are treated as the raw binary.
func extractBinary(assetName string, data []byte, bin string) ([]byte, error) {
	if !strings.HasSuffix(assetName, ".tar.gz") && !strings.HasSuffix(assetName, ".tgz") {
		return data, nil
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%s: gzip: %w", bin, err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: tar: %w", bin, err)
		}
		if hdr.FileInfo().IsDir() {
			continue
		}
		if filepath.Base(hdr.Name) == bin {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("%s: %s not found in %s", bin, bin, assetName)
}

// stageAndRename lands data on destDir/bin (0755) atomically via
// tools.WriteFileAtomic: parent dirs are created, the temp is unique and
// fsynced, and a failed write never leaves a partial binary at destDir/bin.
func stageAndRename(destDir, bin string, data []byte) error {
	return tools.WriteFileAtomic(filepath.Join(destDir, bin), data, 0o755)
}

// verifyExtractInstall verifies assetBytes against the checksum for assetName
// found in sumsBytes (FAIL-CLOSED), extracts bin, and lands it atomically in
// destDir. Nothing is written when verification fails.
func verifyExtractInstall(assetName string, assetBytes []byte, sumsBytes []byte, bin, destDir string) error {
	want, err := checksumHex(sumsBytes, assetName)
	if err != nil {
		return fmt.Errorf("verify %s: %w", bin, err)
	}
	got := sha256.Sum256(assetBytes)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("verify %s: checksum mismatch for %s", bin, assetName)
	}
	binBytes, err := extractBinary(assetName, assetBytes, bin)
	if err != nil {
		return err
	}
	return stageAndRename(destDir, bin, binBytes)
}

// VerifyAsset checks a release asset without extracting or writing it.
func VerifyAsset(name string, data, sums []byte) error {
	want, err := checksumHex(sums, name)
	if err != nil {
		return err
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("checksum mismatch for %s", name)
	}
	return nil
}
