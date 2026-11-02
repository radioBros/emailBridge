package connect

import (
	"math"
	"regexp"
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
)

var hexColourRE = regexp.MustCompile(`(?i)^#[0-9a-f]{6}$`)

// Allowed palette keys (VuetifyThemes.ts + W61).
var paletteKeys = map[string]bool{
	"primary": true, "primary-darken-1": true, "on-primary": true,
	"secondary": true, "on-secondary": true,
	"accent": true, "on-accent": true,
	"success": true, "on-success": true,
	"info": true, "on-info": true,
	"warning": true, "on-warning": true,
	"error": true, "on-error": true,
	"background": true, "on-background": true,
	"surface": true, "on-surface": true,
	"surface-bright": true, "surface-variant": true, "on-surface-variant": true,
	"skeleton": true, "skeleton-highlight": true, "field": true,
	"icon-indigo": true, "icon-cyan": true, "icon-violet": true,
	"icon-teal": true, "icon-amber": true, "icon-rose": true,
}

var fillKeys = []string{"primary", "secondary", "accent", "success", "info", "warning", "error"}

// validatePalette checks hex format and WCAG AA for body-text pairs that the app relies on.
// Only keys present in the patch (merged with existing for the mode being validated) are checked
// when both sides of a pair are known after merge.
func validatePalette(p *BrandingPalette) error {
	if p == nil {
		return nil
	}
	if err := validatePaletteMode(p.Light); err != nil {
		return err
	}
	return validatePaletteMode(p.Dark)
}

func validatePaletteMode(m map[string]string) error {
	if m == nil {
		return nil
	}
	norm := map[string]string{}
	for k, v := range m {
		key := strings.TrimSpace(k)
		if !paletteKeys[key] {
			return errs.New(errs.CodeValidationFailed, "Unknown palette colour key.").WithSafe("key", key)
		}
		col := strings.TrimSpace(v)
		if col == "" {
			continue
		}
		if !hexColourRE.MatchString(col) {
			return errs.New(errs.CodeValidationFailed, "Palette colours must be #RRGGBB hex.").WithSafe("key", key)
		}
		norm[key] = strings.ToUpper(col)
	}
	check := func(fg, bg string) error {
		a, okA := norm[fg]
		b, okB := norm[bg]
		if !okA || !okB {
			return nil
		}
		if contrastRatio(a, b) < 4.5 {
			return errs.New(errs.CodeBrandingPaletteContrast,
				"Palette colour pair fails WCAG AA (4.5:1).").WithSafe("fg", fg).WithSafe("bg", bg)
		}
		return nil
	}
	if err := check("on-surface", "surface"); err != nil {
		return err
	}
	if err := check("on-background", "background"); err != nil {
		return err
	}
	if err := check("on-surface-variant", "surface-variant"); err != nil {
		return err
	}
	for _, k := range fillKeys {
		if err := check("on-"+k, k); err != nil {
			return err
		}
	}
	return nil
}

func mergePalette(existing map[string]any, patch *BrandingPalette) map[string]any {
	out := map[string]any{}
	for k, v := range existing {
		out[k] = v
	}
	if patch == nil {
		return out
	}
	if patch.Light != nil {
		out["light"] = mergeColourMap(asStringMap(out["light"]), patch.Light)
	}
	if patch.Dark != nil {
		out["dark"] = mergeColourMap(asStringMap(out["dark"]), patch.Dark)
	}
	return out
}

func mergeColourMap(base, patch map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range patch {
		v = strings.TrimSpace(v)
		if v == "" {
			delete(out, k)
			continue
		}
		out[k] = strings.ToUpper(v)
	}
	return out
}

func asStringMap(v any) map[string]string {
	switch m := v.(type) {
	case map[string]string:
		return m
	case map[string]any:
		out := map[string]string{}
		for k, raw := range m {
			if s, ok := raw.(string); ok {
				out[k] = s
			}
		}
		return out
	default:
		return map[string]string{}
	}
}

func paletteFromBrand(brand map[string]any) *BrandingPalette {
	if brand == nil {
		return nil
	}
	raw, ok := brand["palette"]
	if !ok || raw == nil {
		return nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		// bson may decode as map[string]interface{} already handled; also nested typed maps
		if typed, ok := raw.(map[string]string); ok {
			_ = typed
		}
		return nil
	}
	p := &BrandingPalette{
		Light: asStringMap(m["light"]),
		Dark:  asStringMap(m["dark"]),
	}
	if len(p.Light) == 0 {
		p.Light = nil
	}
	if len(p.Dark) == 0 {
		p.Dark = nil
	}
	if p.Light == nil && p.Dark == nil {
		return nil
	}
	return p
}

func contrastRatio(a, b string) float64 {
	l1 := relativeLuminance(a)
	l2 := relativeLuminance(b)
	lighter := math.Max(l1, l2)
	darker := math.Min(l1, l2)
	return (lighter + 0.05) / (darker + 0.05)
}

func relativeLuminance(hex string) float64 {
	hex = strings.TrimPrefix(strings.ToLower(hex), "#")
	if len(hex) != 6 {
		return 0
	}
	var r, g, b int64
	_, _ = parseHexByte(hex[0:2], &r)
	_, _ = parseHexByte(hex[2:4], &g)
	_, _ = parseHexByte(hex[4:6], &b)
	rf := srgbLin(float64(r) / 255)
	gf := srgbLin(float64(g) / 255)
	bf := srgbLin(float64(b) / 255)
	return 0.2126*rf + 0.7152*gf + 0.0722*bf
}

func parseHexByte(s string, out *int64) (int, error) {
	var v int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		var d int64
		switch {
		case c >= '0' && c <= '9':
			d = int64(c - '0')
		case c >= 'a' && c <= 'f':
			d = int64(c - 'a' + 10)
		default:
			return 0, errs.New(errs.CodeValidationFailed, "bad hex")
		}
		v = v*16 + d
	}
	*out = v
	return 0, nil
}

func srgbLin(c float64) float64 {
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}
