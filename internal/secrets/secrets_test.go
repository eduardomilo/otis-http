package secrets

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestMemory(t *testing.T) {
	var s Store = NewMemory()
	if _, err := s.Get("x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get missing = %v, want ErrNotFound", err)
	}
	if err := s.Delete("x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete missing = %v, want ErrNotFound", err)
	}
	for k, v := range map[string]string{"api/dev/token": "t1", "api/prod/token": "t2", "other/dev/x": "y"} {
		if err := s.Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if v, err := s.Get("api/dev/token"); err != nil || v != "t1" {
		t.Errorf("Get = %q %v", v, err)
	}
	if err := s.Set("api/dev/token", "t1b"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Get("api/dev/token"); v != "t1b" {
		t.Errorf("overwrite failed: %q", v)
	}
	keys, err := s.List("api/")
	if err != nil || !reflect.DeepEqual(keys, []string{"api/dev/token", "api/prod/token"}) {
		t.Errorf("List(api/) = %v %v", keys, err)
	}
	all, _ := s.List("")
	if len(all) != 3 {
		t.Errorf("List() = %v", all)
	}
	if err := s.Delete("api/dev/token"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("api/dev/token"); !errors.Is(err, ErrNotFound) {
		t.Error("still present after delete")
	}
}

func TestKey(t *testing.T) {
	if got := Key("api", "dev", "token"); got != "api/dev/token" {
		t.Errorf("Key = %q", got)
	}
}

func TestMemoryConcurrent(t *testing.T) {
	s := NewMemory()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k := Key("c", "e", string(rune('a'+i%26)))
			_ = s.Set(k, "v")
			_, _ = s.Get(k)
			_, _ = s.List("c/")
		}(i)
	}
	wg.Wait()
}
