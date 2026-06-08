package license

import (
	"encoding/asn1"
	"testing"
)

func TestParseAppAttestRiskMetric(t *testing.T) {
	value, err := asn1.Marshal("5")
	if err != nil {
		t.Fatal(err)
	}
	receipt := append([]byte{0x30, 0x80}, []byte{
		0x30, byte(2 + 3 + 2 + len(value)),
		0x02, 0x01, 0x11,
		0x02, 0x01, 0x01,
		0x04, byte(len(value)),
	}...)
	receipt = append(receipt, value...)

	metric, err := parseAppAttestRiskMetric(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if metric == nil || *metric != 5 {
		t.Fatalf("metric = %v, want 5", metric)
	}
}
