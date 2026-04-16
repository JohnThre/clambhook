package cnet

import (
	"encoding/hex"
	"testing"
)

func TestSHA224(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "empty string",
			input:  "",
			expect: "d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f",
		},
		{
			name:   "abc",
			input:  "abc",
			expect: "23097d223405d8228642a477bda255b32aadbce4bda0b3f7e36c9da7",
		},
		{
			name:   "448-bit message",
			input:  "abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq",
			expect: "75388b16512776cc5dba5da1fd890150b0c6455cb4f58b1952522525",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := hex.EncodeToString(SHA224([]byte(tc.input)))
			if got != tc.expect {
				t.Errorf("SHA224(%q)\n  got  %s\n  want %s", tc.input, got, tc.expect)
			}
		})
	}
}
