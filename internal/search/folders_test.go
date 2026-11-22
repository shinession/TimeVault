package search

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMockFolderListerAndProber(t *testing.T) {
	m := NewMock(nil)
	m.SetSubfolders(`D:\data\`, []FolderInfo{{Name: "a", Size: 1}, {Name: "b", Size: 2}})
	got, more, err := m.Subfolders(context.Background(), `d:\DATA`, 0)
	if err != nil || more || len(got) != 2 {
		t.Fatalf("Subfolders = %+v, %v, %v", got, more, err)
	}
	if got, more, _ = m.Subfolders(context.Background(), `D:\data`, 1); !more || len(got) != 1 {
		t.Fatalf("limited Subfolders = %+v, more=%v", got, more)
	}
	m.SetSubfolders(`D:\data`, nil)
	if got, _, _ = m.Subfolders(context.Background(), `D:\data`, 0); len(got) != 0 {
		t.Fatalf("a removed folder still lists %+v", got)
	}

	if _, err := m.Probe(context.Background(), `D:\data`, time.Now()); !errors.Is(err, ErrNotIndexed) {
		t.Fatalf("Probe of a folder that was not set = %v", err)
	}
	m.SetProbe(`D:\data`, ProbeResult{Items: 3, Changed: true})
	if res, err := m.Probe(context.Background(), `D:\DATA\`, time.Now()); err != nil || res.Items != 3 || !res.Changed {
		t.Fatalf("Probe = %+v, %v", res, err)
	}
	m.SetUnavailable(true)
	if _, err := m.Probe(context.Background(), `D:\data`, time.Now()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Probe while down = %v", err)
	}
	if _, _, err := m.Subfolders(context.Background(), `D:\data`, 0); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Subfolders while down = %v", err)
	}
	if m.ProbeCalls() != 3 {
		t.Errorf("ProbeCalls = %d, want 3", m.ProbeCalls())
	}
}
