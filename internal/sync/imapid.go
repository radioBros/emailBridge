package sync

// MergeIMAPID overlays account ID fields onto the global EB_IMAP_ID_* map (W57 / G18).
// Non-empty account keys win. Returns a copy; never mutates global.
func MergeIMAPID(global map[string]string, account map[string]string) map[string]string {
	if len(global) == 0 && len(account) == 0 {
		return nil
	}
	out := make(map[string]string, len(global)+len(account))
	for k, v := range global {
		if v != "" {
			out[k] = v
		}
	}
	for k, v := range account {
		if v != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// AccountIMAPID builds the overlay map from per-account IMAP ID fields.
func AccountIMAPID(name, version, vendor, supportURL string) map[string]string {
	m := map[string]string{}
	if name != "" {
		m["name"] = name
	}
	if version != "" {
		m["version"] = version
	}
	if vendor != "" {
		m["vendor"] = vendor
	}
	if supportURL != "" {
		m["support-url"] = supportURL
	}
	if len(m) == 0 {
		return nil
	}
	return m
}
