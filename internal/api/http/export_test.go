package httpapi

// SetHerdrMinBase stands in for the herdrMinBase a release sets, for the
// test's lifetime; the returned func puts the old value back.
func SetHerdrMinBase(v string) (restore func()) {
	old := minBase.Load()
	minBase.Store(&v)
	return func() { minBase.Store(old) }
}
