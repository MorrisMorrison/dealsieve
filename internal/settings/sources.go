package settings

// SourceNames are the known deal sources, in display order. The pepper
// package's site table is tested to list the same names.
var SourceNames = []string{"mydealz", "hotukdeals", "dealabs", "preisjaeger", "chollometro", "pepperpl"}

// Source is one deal source's settings.
type Source struct {
	Enabled        bool `json:"enabled"`
	MinTemperature *int `json:"min_temperature,omitempty"` // nil: the global minimum
}

var sourceLabels = map[string]string{"pepperpl": "pepper.pl"}

// SourceLabel is how the page and messages name a source.
func SourceLabel(name string) string {
	if l, ok := sourceLabels[name]; ok {
		return l
	}
	return name
}

func defaultSources() map[string]Source {
	out := make(map[string]Source, len(SourceNames))
	for _, n := range SourceNames {
		out[n] = Source{Enabled: n == "mydealz"}
	}
	return out
}

// sourcesOff lists every known source as off; Normalize overlays the
// document's entries on it.
func sourcesOff() map[string]Source {
	out := make(map[string]Source, len(SourceNames))
	for _, n := range SourceNames {
		out[n] = Source{}
	}
	return out
}

// EnabledSources lists the enabled sources in SourceNames order.
func (s Settings) EnabledSources() []string {
	var out []string
	for _, n := range SourceNames {
		if s.Sources[n].Enabled {
			out = append(out, n)
		}
	}
	return out
}

// SourceMinimums are the sources' own temperature minimums.
func (s Settings) SourceMinimums() map[string]int {
	out := map[string]int{}
	for n, src := range s.Sources {
		if src.MinTemperature != nil {
			out[n] = *src.MinTemperature
		}
	}
	return out
}
