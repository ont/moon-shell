package gogmoon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type Workspace struct {
	Dir             string
	BodyPath        string
	AttachmentPaths []string
}

func PrepareWorkspace(ctx context.Context, cfg GogConfig, gog *Gog, message Message) (Workspace, error) {
	pattern := cfg.TempPattern
	if pattern == "" {
		pattern = "moon-shell-*"
	}
	dir, err := os.MkdirTemp("/tmp", pattern)
	if err != nil {
		return Workspace{}, fmt.Errorf("create temp directory: %w", err)
	}

	bodyPath := filepath.Join(dir, "body.txt")
	if err := os.WriteFile(bodyPath, []byte(message.Body), 0o600); err != nil {
		return Workspace{}, fmt.Errorf("write message body: %w", err)
	}

	attachmentsDir := filepath.Join(dir, "attachments")
	if err := os.MkdirAll(attachmentsDir, 0o700); err != nil {
		return Workspace{}, fmt.Errorf("create attachments directory: %w", err)
	}

	paths := make([]string, 0, len(message.Attachments))
	for i, attachment := range message.Attachments {
		filename := uniqueAttachmentPath(dir, i, attachment.Filename)
		switch {
		case len(attachment.Data) > 0:
			if err := os.WriteFile(filename, attachment.Data, 0o600); err != nil {
				return Workspace{}, fmt.Errorf("write inline attachment %s: %w", attachment.Filename, err)
			}
		case attachment.AttachmentID != "":
			if err := gog.SaveAttachment(ctx, message.ID, attachment.AttachmentID, filename); err != nil {
				return Workspace{}, err
			}
		default:
			continue
		}
		paths = append(paths, filename)

		archivePath := filepath.Join(attachmentsDir, filepath.Base(filename))
		if err := copyFile(filename, archivePath); err != nil {
			return Workspace{}, err
		}
	}

	return Workspace{Dir: dir, BodyPath: bodyPath, AttachmentPaths: paths}, nil
}

func uniqueAttachmentPath(dir string, index int, filename string) string {
	if filename == "" {
		filename = "attachment"
	}
	if index == 0 {
		return filepath.Join(dir, filename)
	}
	return filepath.Join(dir, fmt.Sprintf("%03d-%s", index+1, filename))
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read attachment copy source %s: %w", src, err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return fmt.Errorf("write attachment copy %s: %w", dst, err)
	}
	return nil
}
