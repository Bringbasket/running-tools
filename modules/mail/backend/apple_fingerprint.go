package mail

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Apple expects its browser fingerprint field to change with the current
// request time. The uncompressed representation is accepted by the same web
// client path and avoids persisting any device-specific identifier.
func appleFDClientInfo(userAgent string) string {
	info := map[string]string{
		"U": firstNonEmpty(userAgent, appleAccountUserAgent),
		"L": "zh-CN",
		"Z": "GMT+08:00",
		"V": "1.1",
		"F": appleCompressedFingerprint(time.Now().In(time.FixedZone("apple-account", 8*60*60))),
	}
	data, _ := json.Marshal(info)
	return string(data)
}

func appleCompressedFingerprint(now time.Time) string {
	raw := appleFingerprintPayload(now)
	replaced := raw
	for index, token := range appleFingerprintDictionary {
		replaced = strings.ReplaceAll(replaced, token, string(rune(index+1)))
	}
	encoded, ok := appleFingerprintHuffman(replaced)
	if !ok {
		return raw
	}
	checksum := 65535
	for _, value := range []byte(raw) {
		checksum = ((checksum >> 8) | (checksum << 8)) & 0xffff
		checksum ^= int(value) & 0xff
		checksum ^= (checksum & 0xff) >> 4
		checksum ^= (checksum << 12) & 0xffff
		checksum ^= ((checksum & 0xff) << 5) & 0xffff
	}
	return encoded + string(appleFingerprintAlphabet[(checksum>>12)&63]) + string(appleFingerprintAlphabet[(checksum>>6)&63]) + string(appleFingerprintAlphabet[checksum&63])
}

func appleFingerprintHuffman(value string) (string, bool) {
	var builder strings.Builder
	bitBuffer, bitCount := 0, 0
	push := func(width, code int) {
		bitBuffer = (bitBuffer << width) | code
		bitCount += width
		for bitCount >= 6 {
			index := (bitBuffer >> (bitCount - 6)) & 63
			builder.WriteByte(appleFingerprintAlphabet[index])
			bitCount -= 6
			bitBuffer ^= index << bitCount
		}
	}
	push(6, (len(value)&7)<<3)
	push(6, (len(value)&56)|1)
	for _, current := range value {
		code, ok := appleFingerprintCodes[int(current)]
		if !ok {
			return "", false
		}
		push(code.width, code.value)
	}
	code := appleFingerprintCodes[0]
	push(code.width, code.value)
	if bitCount > 0 {
		push(6-bitCount, 0)
	}
	return builder.String(), true
}

type appleFingerprintCode struct{ width, value int }

var appleFingerprintDictionary = []string{"%20", ";;;", "%3B", "%2C", "und", "fin", "ed;", "%28", "%29", "%3A", "/53", "ike", "Web", "0;", ".0", "e;", "on", "il", "ck", "01", "in", "Mo", "fa", "00", "32", "la", ".1", "ri", "it", "%u", "le"}

const appleFingerprintAlphabet = ".0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ_abcdefghijklmnopqrstuvwxyz"

var appleFingerprintCodes = map[int]appleFingerprintCode{
	1: {4, 15}, 110: {8, 239}, 74: {8, 238}, 57: {7, 118}, 56: {7, 117}, 71: {8, 233}, 25: {8, 232}, 101: {5, 28}, 104: {7, 111}, 4: {7, 110}, 105: {6, 54}, 5: {7, 107},
	109: {7, 106}, 103: {9, 423}, 82: {9, 422}, 26: {8, 210}, 6: {7, 104}, 46: {6, 51}, 97: {6, 50}, 111: {6, 49}, 7: {7, 97}, 45: {7, 96}, 59: {5, 23}, 15: {7, 91},
	11: {8, 181}, 72: {8, 180}, 27: {8, 179}, 28: {8, 178}, 16: {7, 88}, 88: {10, 703}, 113: {11, 1405}, 89: {12, 2809}, 107: {13, 5617}, 90: {14, 11233}, 42: {15, 22465},
	64: {16, 44929}, 0: {16, 44928}, 81: {9, 350}, 29: {8, 174}, 118: {8, 173}, 30: {8, 172}, 98: {8, 171}, 12: {8, 170}, 99: {7, 84}, 117: {6, 41}, 112: {6, 40}, 102: {9, 319},
	68: {9, 318}, 31: {8, 158}, 100: {7, 78}, 84: {6, 38}, 55: {6, 37}, 17: {7, 73}, 8: {7, 72}, 9: {7, 71}, 77: {7, 70}, 18: {7, 69}, 65: {7, 68}, 48: {6, 33},
	116: {6, 32}, 10: {7, 63}, 121: {8, 125}, 78: {8, 124}, 80: {7, 61}, 69: {7, 60}, 119: {7, 59}, 13: {8, 117}, 79: {8, 116}, 19: {7, 57}, 67: {7, 56}, 114: {6, 27},
	83: {6, 26}, 115: {6, 25}, 14: {6, 24}, 122: {8, 95}, 95: {8, 94}, 76: {7, 46}, 24: {7, 45}, 37: {7, 44}, 50: {5, 10}, 51: {5, 9}, 108: {6, 17}, 22: {7, 33},
	120: {8, 65}, 66: {8, 64}, 21: {7, 31}, 106: {7, 30}, 47: {6, 14}, 53: {5, 6}, 49: {5, 5}, 86: {8, 39}, 85: {8, 38}, 23: {7, 18}, 75: {7, 17}, 20: {7, 16},
	2: {5, 3}, 73: {8, 23}, 43: {9, 45}, 87: {9, 44}, 70: {7, 10}, 3: {6, 4}, 52: {5, 1}, 54: {5, 0},
}

func appleFingerprintPayload(now time.Time) string {
	values := []string{"TF1", "020"}
	for range 39 {
		values = append(values, "")
	}
	values = append(values, "true", "true", strconv.FormatInt(now.UnixMilli(), 10), "-6", "6/7/2005, 9:33:44 PM", "", "", "", "", "", "", strconv.FormatInt(now.UnixMilli(), 10), "0", appleUSLocaleTime(now))
	for range 34 {
		values = append(values, "")
	}
	values = append(values, "5.6.1-0", "")
	var builder strings.Builder
	for _, value := range values {
		builder.WriteString(urlEscapeAppleFingerprint(value))
		builder.WriteByte(';')
	}
	return builder.String()
}

func appleUSLocaleTime(value time.Time) string {
	hour, suffix := value.Hour(), "AM"
	if hour >= 12 {
		suffix = "PM"
	}
	hour %= 12
	if hour == 0 {
		hour = 12
	}
	return fmt.Sprintf("%d/%d/%d, %d:%02d:%02d %s", int(value.Month()), value.Day(), value.Year(), hour, value.Minute(), value.Second(), suffix)
}

func urlEscapeAppleFingerprint(value string) string {
	const hex = "0123456789ABCDEF"
	var builder strings.Builder
	for _, current := range value {
		if (current >= 'A' && current <= 'Z') || (current >= 'a' && current <= 'z') || (current >= '0' && current <= '9') || strings.ContainsRune("@*_+-./", current) {
			builder.WriteRune(current)
			continue
		}
		if current <= 0xff {
			builder.WriteByte('%')
			builder.WriteByte(hex[(current>>4)&0xf])
			builder.WriteByte(hex[current&0xf])
			continue
		}
		builder.WriteString("%u")
		builder.WriteByte(hex[(current>>12)&0xf])
		builder.WriteByte(hex[(current>>8)&0xf])
		builder.WriteByte(hex[(current>>4)&0xf])
		builder.WriteByte(hex[current&0xf])
	}
	return builder.String()
}
