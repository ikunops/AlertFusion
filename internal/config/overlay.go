package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// NotificationOverlayPath returns the UI-editable notification settings file.
// Kept under the same directory as mute store so Docker only needs a writable data volume.
func NotificationOverlayPath(muteStorePath string) string {
	dir := filepath.Dir(muteStorePath)
	if dir == "" || dir == "." {
		return filepath.Join("data", "notification.yaml")
	}
	return filepath.Join(dir, "notification.yaml")
}

// ApplyNotificationOverlay overlays Web-saved notification settings onto cfg if the file exists.
func ApplyNotificationOverlay(path string, cfg *Config) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read notification overlay: %w", err)
	}
	if len(data) == 0 {
		return false, nil
	}
	var n NotificationConfig
	if err := yaml.Unmarshal(data, &n); err != nil {
		return false, fmt.Errorf("parse notification overlay: %w", err)
	}
	// Preserve retry defaults if overlay omitted them.
	if n.Retry.Count == 0 {
		n.Retry.Count = cfg.Notification.Retry.Count
	}
	if n.Retry.Interval.Duration == 0 {
		n.Retry.Interval = cfg.Notification.Retry.Interval
	}
	cfg.Notification = n
	cfg.applyDefaults()
	return true, nil
}

// SaveNotificationOverlay persists notification settings for the Web UI.
func SaveNotificationOverlay(path string, n NotificationConfig) error {
	if path == "" {
		return fmt.Errorf("notification overlay path is empty")
	}
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir notification overlay: %w", err)
		}
	}
	data, err := yaml.Marshal(n)
	if err != nil {
		return fmt.Errorf("marshal notification overlay: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		// Directory may be writable but rename across mounts can fail; try direct write.
		if werr := os.WriteFile(path, data, 0o644); werr != nil {
			return fmt.Errorf("write notification overlay: %w", err)
		}
		return nil
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		if werr := os.WriteFile(path, data, 0o644); werr != nil {
			return fmt.Errorf("write notification overlay: %w", werr)
		}
	}
	return nil
}
