package proxyd

import "sync"

type StringMap struct {
	underlying map[string]string
	mtx        sync.RWMutex
}

func NewStringMap() *StringMap {
	return &StringMap{
		underlying: make(map[string]string),
	}
}

func (s *StringMap) Get(key string) (string, bool) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	v := ""
	v, has := s.underlying[key]

	return v, has
}

func (s *StringMap) GetAndRemove(key string) (string, bool) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	v := ""
	v, has := s.underlying[key]

	if has {
		delete(s.underlying, key)
	}

	return v, has
}

func (s *StringMap) Set(key string, value string) {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	s.underlying[key] = value
}

func (s *StringMap) Entries() []string {
	s.mtx.RLock()
	defer s.mtx.RUnlock()
	out := make([]string, len(s.underlying))
	var i int
	for entry := range s.underlying {
		out[i] = entry
		i++
	}
	return out
}
