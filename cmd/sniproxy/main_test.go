package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func buildClientHello(sni string) []byte {
	var b bytes.Buffer

	b.WriteByte(22)

	b.Write([]byte{0x03, 0x01})

	payload := buildClientHelloPayload(sni)
	length := make([]byte, 2)
	binary.BigEndian.PutUint16(length, uint16(len(payload)))
	b.Write(length)
	b.Write(payload)

	return b.Bytes()
}

func buildClientHelloPayload(sni string) []byte {
	var p bytes.Buffer

	p.WriteByte(0x01)

	handshakeLen := make([]byte, 3)
	body := buildClientHelloBody(sni)
	binary.BigEndian.PutUint16(handshakeLen[1:3], uint16(len(body)))
	handshakeLen[0] = 0
	if len(body) > 0xFFFF {
		handshakeLen[0] = byte(len(body) >> 16)
	}
	p.Write(handshakeLen)
	p.Write(body)

	return p.Bytes()
}

func buildClientHelloBody(sni string) []byte {
	var b bytes.Buffer

	b.Write([]byte{0x03, 0x03})

	random := make([]byte, 32)
	for i := range random {
		random[i] = byte(i)
	}
	b.Write(random)

	b.WriteByte(0)

	cipherSuites := []uint16{
		0x1301, 0x1302, 0x1303, 0xc02b, 0xc02f, 0xc02c, 0xc030,
	}
	csLen := len(cipherSuites) * 2
	b.Write([]byte{byte(csLen >> 8), byte(csLen & 0xff)})
	for _, cs := range cipherSuites {
		binary.Write(&b, binary.BigEndian, cs)
	}

	b.WriteByte(0)

	sniNameBytes := []byte(sni)
	sniListLen := 1 + 2 + len(sniNameBytes)
	extDataLen := 2 + sniListLen
	extTotalLen := 4 + extDataLen

	b.Write([]byte{byte(extTotalLen >> 8), byte(extTotalLen & 0xff)})

	b.Write([]byte{0x00, 0x00})

	b.Write([]byte{byte(extDataLen >> 8), byte(extDataLen & 0xff)})

	b.Write([]byte{byte(sniListLen >> 8), byte(sniListLen & 0xff)})
	b.WriteByte(0)
	b.Write([]byte{byte(len(sniNameBytes) >> 8), byte(len(sniNameBytes) & 0xff)})
	b.Write(sniNameBytes)

	return b.Bytes()
}

func TestExtractSNI(t *testing.T) {
	tests := []struct {
		name    string
		sni     string
		wantErr bool
	}{
		{"simple hostname", "www.example.com", false},
		{"nested subdomain", "sub.domain.example.com", false},
		{"single label", "localhost", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hello := buildClientHelloBody(tc.sni)
			got, err := extractSNI(hello)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got SNI %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.sni {
				t.Errorf("extractSNI = %q, want %q", got, tc.sni)
			}
		})
	}
}

func TestExtractSNI_TooShortPayload(t *testing.T) {
	_, err := extractSNI(nil)
	if err == nil {
		t.Error("expected error for nil payload")
	}

	_, err = extractSNI([]byte{0x03})
	if err == nil {
		t.Error("expected error for truncated payload")
	}
}

func TestParseSNI(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    string
		wantErr bool
	}{
		{
			name: "valid hostname",
			data: func() []byte {
				d := make([]byte, 2)
				binary.BigEndian.PutUint16(d, uint16(5+1+2))
				d = append(d, 0)
				d = append(d, 0x00, 0x05)
				d = append(d, []byte("hello")...)
				return d
			}(),
			want: "hello",
		},
		{
			name:    "empty list",
			data:    []byte{0x00, 0x00},
			wantErr: true,
		},
		{
			name:    "truncated",
			data:    []byte{0x00, 0x05, 0x00},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSNI(tc.data)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got SNI %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("parseSNI = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadRecordAndExtractSNI(t *testing.T) {
	sni := "test.example.com"
	raw := buildClientHello(sni)
	r := bytes.NewReader(raw)

	record, err := readRecord(r)
	if err != nil {
		t.Fatalf("readRecord failed: %v", err)
	}
	if record.ContentType != recordTypeHandshake {
		t.Errorf("content type = %d, want %d", record.ContentType, recordTypeHandshake)
	}

	payload := record.Payload
	if len(payload) < 4 {
		t.Fatal("payload too short")
	}
	helloBody := payload[4:]
	handshakeLen := uint24(payload[1:4])
	if len(helloBody) < handshakeLen {
		t.Fatalf("truncated handshake body: %d < %d", len(helloBody), handshakeLen)
	}
	helloBody = helloBody[:handshakeLen]

	got, err := extractSNI(helloBody)
	if err != nil {
		t.Fatalf("extractSNI failed: %v", err)
	}
	if got != sni {
		t.Errorf("extractSNI = %q, want %q", got, sni)
	}
}

func TestReadRecord_NotHandshake(t *testing.T) {
	raw := []byte{0x17, 0x03, 0x03, 0x00, 0x01, 0x00}
	r := bytes.NewReader(raw)

	record, err := readRecord(r)
	if err != nil {
		t.Fatalf("readRecord failed: %v", err)
	}
	if record.ContentType == recordTypeHandshake {
		t.Error("expected non-handshake content type")
	}
}

func TestReadRecord_EmptyInput(t *testing.T) {
	r := bytes.NewReader(nil)
	_, err := readRecord(r)
	if err == nil {
		t.Error("expected error for empty input")
	}
}

func TestReadRecord_TruncatedPayload(t *testing.T) {
	raw := []byte{22, 0x03, 0x01, 0x00, 0x05, 0x01, 0x02}
	r := bytes.NewReader(raw)

	_, err := readRecord(r)
	if err == nil {
		t.Error("expected error for truncated payload")
	}
}

func TestIsTimeoutErr(t *testing.T) {
	if isTimeoutErr(nil) {
		t.Error("nil should not be timeout")
	}
}

func TestUint24(t *testing.T) {
	tests := []struct {
		input []byte
		want  int
	}{
		{[]byte{0x00, 0x00, 0x05}, 5},
		{[]byte{0x01, 0x00, 0x00}, 65536},
		{[]byte{0x10, 0x00, 0x00}, 1048576},
		{nil, 0},
		{[]byte{0x01, 0x02}, 0},
	}

	for _, tc := range tests {
		got := uint24(tc.input)
		if got != tc.want {
			t.Errorf("uint24(%v) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestEnv(t *testing.T) {
	if got := env("NONEXISTENT_VAR_XYZ", "fallback"); got != "fallback" {
		t.Errorf("env() = %q, want %q", got, "fallback")
	}
	t.Setenv("TEST_EXISTING_VAR", "value")
	if got := env("TEST_EXISTING_VAR", "fallback"); got != "value" {
		t.Errorf("env() = %q, want %q", got, "value")
	}
}
