package validators

import "testing"

//nat:validator id:gate1 type:gate
func TestGate1(t *testing.T) {
	t.Log("Gate 1 running")
}

//nat:validator id:suite1 type:suite gate:gate1
func TestSuite1(t *testing.T) {
	t.Log("Suite 1 running")
}

//nat:validator id:test1 type:test gate:gate1 suite:suite1
func TestInSuite(t *testing.T) {
	t.Log("Test in suite running")
}

//nat:validator id:test2 type:test gate:gate1
func TestDirectToGate(t *testing.T) {
	t.Log("Direct to gate test running")
}
