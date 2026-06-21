package daemon

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CreateTarGz archives srcDir to destFile and returns SHA256 checksum and size.
func CreateTarGz(srcDir, destFile string) (string, int64, error) {
	// Create dest file
	f, err := os.Create(destFile)
	if err != nil {
		return "", 0, fmt.Errorf("failed to create archive file: %w", err)
	}
	defer f.Close()

	// Set up SHA256 hashing writer on the compressed output stream
	hash := sha256.New()
	mw := io.MultiWriter(f, hash)

	gw := gzip.NewWriter(mw)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	// Walk source directory
	err = filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get path relative to the source directory to preserve structure
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil // Skip root folder itself
		}

		// Create header
		header, err := tar.FileInfoHeader(info, info.Name())
		if err != nil {
			return fmt.Errorf("failed to create tar header for %s: %w", path, err)
		}
		header.Name = relPath

		// Write header
		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("failed to write header for %s: %w", path, err)
		}

		// If it's a directory, we don't need to copy file contents
		if info.IsDir() {
			return nil
		}

		// Copy file content
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", path, err)
		}
		defer file.Close()

		if _, err := io.Copy(tw, file); err != nil {
			return fmt.Errorf("failed to copy file %s to archive: %w", path, err)
		}

		return nil
	})

	if err != nil {
		return "", 0, err
	}

	// Close archive writers to ensure everything is flushed to disk
	if err := tw.Close(); err != nil {
		return "", 0, fmt.Errorf("failed to close tar writer: %w", err)
	}
	if err := gw.Close(); err != nil {
		return "", 0, fmt.Errorf("failed to close gzip writer: %w", err)
	}
	if err := f.Sync(); err != nil {
		return "", 0, fmt.Errorf("failed to sync file: %w", err)
	}

	// Get file size
	stat, err := f.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("failed to stat file: %w", err)
	}

	checksum := hex.EncodeToString(hash.Sum(nil))
	return checksum, stat.Size(), nil
}

// ExtractTarGz extracts tar.gz from srcFile into destDir.
func ExtractTarGz(srcFile, destDir string) error {
	f, err := os.Open(srcFile)
	if err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	// Ensure destination directory exists
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read next tar entry: %w", err)
		}

		target := filepath.Join(destDir, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", target, err)
			}
		case tar.TypeReg:
			// Ensure parent dir exists (tar might not always list dir headers before file headers)
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("failed to create parent dir for %s: %w", target, err)
			}

			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("failed to open destination file %s: %w", target, err)
			}
			defer outFile.Close()

			if _, err := io.Copy(outFile, tr); err != nil {
				outFile.Close()
				return fmt.Errorf("failed to extract file %s: %w", target, err)
			}
			outFile.Close()
		default:
			return fmt.Errorf("unsupported type flag %b in archive for %s", header.Typeflag, header.Name)
		}
	}

	return nil
}

// ComputeFileSha256 calculates SHA256 of file.
func ComputeFileSha256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// CompressFileToGzip compresses a source file to a gzip destFile and returns SHA256 checksum and size of the compressed file.
func CompressFileToGzip(srcFile, destFile string) (string, int64, error) {
	sf, err := os.Open(srcFile)
	if err != nil {
		return "", 0, fmt.Errorf("failed to open source file: %w", err)
	}
	defer sf.Close()

	df, err := os.Create(destFile)
	if err != nil {
		return "", 0, fmt.Errorf("failed to create destination file: %w", err)
	}
	defer df.Close()

	hash := sha256.New()
	mw := io.MultiWriter(df, hash)

	gw := gzip.NewWriter(mw)
	defer gw.Close()

	if _, err := io.Copy(gw, sf); err != nil {
		return "", 0, fmt.Errorf("failed to compress file: %w", err)
	}

	if err := gw.Close(); err != nil {
		return "", 0, fmt.Errorf("failed to close gzip writer: %w", err)
	}

	if err := df.Sync(); err != nil {
		return "", 0, fmt.Errorf("failed to sync file: %w", err)
	}

	stat, err := df.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("failed to stat file: %w", err)
	}

	checksum := hex.EncodeToString(hash.Sum(nil))
	return checksum, stat.Size(), nil
}

// DecompressGzipToFile decompresses a gzip source file to a destFile.
func DecompressGzipToFile(srcFile, destFile string) error {
	sf, err := os.Open(srcFile)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer sf.Close()

	gr, err := gzip.NewReader(sf)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gr.Close()

	df, err := os.OpenFile(destFile, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer df.Close()

	if _, err := io.Copy(df, gr); err != nil {
		return fmt.Errorf("failed to decompress file: %w", err)
	}

	return nil
}

