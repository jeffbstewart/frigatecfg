package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const canonicalYAML = `# canonical: structure, owned by git
mqtt:
  enabled: false
detectors:
  ov:
    type: openvino
    device: GPU
cameras:
  garage:
    ffmpeg:
      inputs:
        - path: rtsp://{FRIGATE_GARAGE_USER}:{FRIGATE_GARAGE_PASSWORD}@cam/main
          roles: [record]
    detect:
      width: 640   # match the sub stream
      height: 480
      fps: 5
`

const tuningYAML = `cameras:
  garage:
    motion:
      mask:
        - "0.86,0.027,0.861,0.086,0.895,0.09,0.893,0.023"
      threshold: 40
`

func mustParse(t *testing.T, s string) *yaml.Node {
	t.Helper()
	d, err := parseDoc([]byte(s), "test")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mustWrite(t *testing.T, d *yaml.Node) string {
	t.Helper()
	b, err := writeDoc(d)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRenderAppliesTuningAndKeepsComments(t *testing.T) {
	out, err := render(mustParse(t, canonicalYAML), mustParse(t, tuningYAML))
	if err != nil {
		t.Fatal(err)
	}
	s := mustWrite(t, out)
	for _, want := range []string{
		"# canonical: structure, owned by git",
		"# match the sub stream",
		`- "0.86,0.027,0.861,0.086,0.895,0.09,0.893,0.023"`,
		"threshold: 40",
		"{FRIGATE_GARAGE_USER}",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("render output missing %q:\n%s", want, s)
		}
	}
}

func TestRenderRejectsStructuralTuning(t *testing.T) {
	bad := mustParse(t, "cameras:\n  garage:\n    detect:\n      fps: 10\n")
	if _, err := render(mustParse(t, canonicalYAML), bad); err == nil {
		t.Fatal("expected error for non-owned path in tuning")
	} else if !strings.Contains(err.Error(), "cameras.garage.detect.fps") {
		t.Errorf("error should name the path: %v", err)
	}
}

func TestMergeLiveWinsOnOwnedGitWinsOnStructure(t *testing.T) {
	live := mustParse(t, `version: 0.17-0
mqtt:
  enabled: true
cameras:
  garage:
    detect:
      width: 1280
      height: 720
      fps: 5
    motion:
      mask:
        - "0,0,0.5,0,0.5,0.1,0,0.1"
      threshold: 55
    zones:
      driveway:
        coordinates: "0,1,1,1,1,0.5,0,0.5"
`)
	out, err := merge(mustParse(t, canonicalYAML), mustParse(t, tuningYAML), live)
	if err != nil {
		t.Fatal(err)
	}
	check := func(p string, want string) {
		t.Helper()
		got := oneLine(get(out, parsePattern(p)))
		if got != want {
			t.Errorf("%s = %q, want %q", p, got, want)
		}
	}
	check("cameras.garage.motion.threshold", "55")                   // live beats tuning
	check("cameras.garage.motion.mask", `- 0,0,0.5,0,0.5,0.1,0,0.1`) // live beats tuning
	check("cameras.garage.zones.driveway.coordinates", "0,1,1,1,1,0.5,0,0.5")
	check("version", "0.17-0")                  // migration key survives
	check("mqtt.enabled", "false")              // structure: git wins
	check("cameras.garage.detect.width", "640") // structure: git wins
	if get(out, parsePattern("detectors.ov.device")) == nil {
		t.Error("structural key from canonical missing after merge")
	}
}

func TestMergeKeepsTuningWhenLiveLacksIt(t *testing.T) {
	live := mustParse(t, "cameras:\n  garage:\n    detect:\n      fps: 5\n")
	out, err := merge(mustParse(t, canonicalYAML), mustParse(t, tuningYAML), live)
	if err != nil {
		t.Fatal(err)
	}
	if got := oneLine(get(out, parsePattern("cameras.garage.motion.threshold"))); got != "40" {
		t.Errorf("tuning value lost when live lacks it: %q", got)
	}
}

func TestPullExtractsOnlyOwned(t *testing.T) {
	live := mustParse(t, `mqtt:
  enabled: true
cameras:
  garage:
    detect:
      fps: 5
    motion:
      threshold: 55
    objects:
      filters:
        person:
          min_area: 500
          mask: "0,0,1,0,1,0.1"
camera_groups:
  outside:
    cameras: [garage]
    order: 1
`)
	out, err := pull(live)
	if err != nil {
		t.Fatal(err)
	}
	s := mustWrite(t, out)
	for _, want := range []string{"threshold: 55", `mask: "0,0,1,0,1,0.1"`, "outside:"} {
		if !strings.Contains(s, want) {
			t.Errorf("pull missing %q:\n%s", want, s)
		}
	}
	for _, bad := range []string{"mqtt", "fps", "min_area"} {
		if strings.Contains(s, bad) {
			t.Errorf("pull leaked structural key %q:\n%s", bad, s)
		}
	}
	if err := validateTuning(out); err != nil {
		t.Errorf("pull output must be a valid tuning doc: %v", err)
	}
}

func TestDiffClassifies(t *testing.T) {
	live := mustParse(t, `mqtt:
  enabled: true
detectors:
  ov:
    type: openvino
    device: GPU
cameras:
  garage:
    ffmpeg:
      inputs:
        - path: rtsp://{FRIGATE_GARAGE_USER}:{FRIGATE_GARAGE_PASSWORD}@cam/main
          roles: [record]
    detect:
      width: 640
      height: 480
      fps: 5
    motion:
      mask:
        - "0.86,0.027,0.861,0.086,0.895,0.09,0.893,0.023"
      threshold: 60
`)
	drifts, err := diff(mustParse(t, canonicalYAML), mustParse(t, tuningYAML), live)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, d := range drifts {
		got[d.Path] = d.Owned
	}
	if owned, ok := got["cameras.garage.motion.threshold"]; !ok || !owned {
		t.Errorf("threshold should be tuning drift: %+v", drifts)
	}
	if owned, ok := got["mqtt.enabled"]; !ok || owned {
		t.Errorf("mqtt.enabled should be structural drift: %+v", drifts)
	}
	if _, ok := got["cameras.garage.motion.mask"]; ok {
		t.Errorf("identical mask reported as drift: %+v", drifts)
	}
	if len(drifts) != 2 {
		t.Errorf("want exactly 2 drifts, got %d: %+v", len(drifts), drifts)
	}
}

func TestDiffCleanWhenLiveEqualsRender(t *testing.T) {
	c, tn := mustParse(t, canonicalYAML), mustParse(t, tuningYAML)
	rendered, err := render(c, tn)
	if err != nil {
		t.Fatal(err)
	}
	live := mustParse(t, mustWrite(t, rendered))
	drifts, err := diff(c, tn, live)
	if err != nil {
		t.Fatal(err)
	}
	if len(drifts) != 0 {
		t.Errorf("expected no drift, got %+v", drifts)
	}
}

func TestOwnedPatterns(t *testing.T) {
	cases := map[string]bool{
		"cameras.garage.motion.mask":                     true,
		"cameras.garage.motion.threshold":                true,
		"cameras.garage.zones.driveway.coordinates":      true,
		"cameras.garage.objects.filters.person.mask":     true,
		"cameras.garage.objects.filters.person.min_area": false,
		"cameras.garage.review.alerts.required_zones":    true,
		"cameras.garage.detect.fps":                      false,
		"cameras.garage.ffmpeg.inputs":                   false,
		"model.path":                                     false,
		"version":                                        true,
		"camera_groups.outside.order":                    true,
		"semantic_search.model_size":                     true,
		"semantic_search.reindex":                        false,
	}
	for p, want := range cases {
		if got := isOwned(parsePattern(p)); got != want {
			t.Errorf("isOwned(%s) = %v, want %v", p, got, want)
		}
	}
}
