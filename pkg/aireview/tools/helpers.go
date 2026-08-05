package tools

import "encoding/json"

// cleanObject removes noisy fields from a Kubernetes object to reduce token usage.
// Modifies the map in place.
func cleanObject(obj map[string]any) {
	if metadata, ok := obj["metadata"].(map[string]any); ok {
		delete(metadata, "managedFields")
		if annotations, ok := metadata["annotations"].(map[string]any); ok {
			delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
		}
	}
}

func marshalCompact(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	s := string(b)
	if len(s) > maxManifestBytes {
		s = s[:maxManifestBytes] + "\n\n[truncated — response exceeded size limit]"
	}
	return s, nil
}
