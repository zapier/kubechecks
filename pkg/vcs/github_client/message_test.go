package github_client

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitComment(t *testing.T) {
	t.Parallel()

	thousandA := strings.Repeat("a", 1000)

	tests := []struct {
		name     string
		comment  string
		maxSize  int
		sepEnd   string
		sepStart string
		want     []string
	}{
		{
			name:     "EmptyComment",
			comment:  "",
			maxSize:  10,
			sepEnd:   "-E",
			sepStart: "-S",
			want:     []string{""},
		},
		{
			name:     "ExactFit_NoSplit",
			comment:  "exact_fit",
			maxSize:  len("exact_fit"),
			sepEnd:   "-E",
			sepStart: "-S",
			want:     []string{"exact_fit"},
		},
		{
			name:     "UnderMax_NoSplit",
			comment:  "comment under max size",
			maxSize:  len("comment under max size") + 1,
			sepEnd:   "sepEnd",
			sepStart: "sepStart",
			want:     []string{"comment under max size"},
		},
		{
			name:    "TwoComments",
			comment: thousandA,
			// Force a split by choosing maxSize just under the full length.
			// Calculation:
			//   For a 1000-character comment and maxSize = 1000 - 1 = 999:
			//     - The first chunk raw capacity = 999 - len(sepEnd).
			//       With sepEnd = "-sepEnd" (length 7), firstRawCapacity = 999 - 7 = 992.
			//       Thus, first chunk = thousandA[0:992] + "-sepEnd".
			//     - The remaining raw text is thousandA[992:1000] (8 characters).
			//       For the final chunk, we only need to add the prefix.
			//       Final chunk = sepStart + thousandA[992:].
			maxSize:  len(thousandA) - 1, // 999
			sepEnd:   "-sepEnd",
			sepStart: "-sepStart",
			want: func() []string {
				firstRawCapacity := 999 - len("-sepEnd") // 999 - 7 = 992
				firstChunk := thousandA[:firstRawCapacity] + "-sepEnd"
				secondChunk := "-sepStart" + thousandA[firstRawCapacity:]
				return []string{firstChunk, secondChunk}
			}(),
		},
		{
			name:     "FourComments",
			comment:  thousandA,
			sepEnd:   "-sepEnd",
			sepStart: "-sepStart",
			// For splitting into four chunks:
			//   Set maxSize = (len(thousandA)/4) + len(sepEnd) + len(sepStart).
			//   For thousandA of length 1000, with sepEnd length 7 and sepStart length 9:
			//     maxSize = (1000/4) + 7 + 9 = 250 + 16 = 266.
			//   Then:
			//     - First chunk raw capacity = 266 - len(sepEnd) = 266 - 7 = 259.
			//       First chunk = thousandA[0:259] + "-sepEnd".
			//     - Subsequent non-final chunks have raw capacity = 266 - 9 - 7 = 250.
			//     - Second chunk = "-sepStart" + thousandA[259:259+250] + "-sepEnd".
			//     - Third chunk = "-sepStart" + thousandA[509:509+250] + "-sepEnd".
			//     - Final chunk = "-sepStart" + thousandA[759:].
			maxSize: (1000 / 4) + 7 + 9, // 266
			want: func() []string {
				firstRawCapacity := 266 - 7 // 259
				chunk1 := thousandA[:firstRawCapacity] + "-sepEnd"
				// For subsequent chunks, raw capacity = 266 - 9 - 7 = 250.
				rawCapacity := 266 - 9 - 7 // 250
				chunk2 := "-sepStart" + thousandA[firstRawCapacity:firstRawCapacity+rawCapacity] + "-sepEnd"
				chunk3 := "-sepStart" + thousandA[firstRawCapacity+rawCapacity:firstRawCapacity+2*rawCapacity] + "-sepEnd"
				chunk4 := "-sepStart" + thousandA[firstRawCapacity+2*rawCapacity:]
				return []string{chunk1, chunk2, chunk3, chunk4}
			}(),
		},
		{
			name:    "MaxSizeTooSmall_ReturnUnsplit",
			comment: "Hello, world!",
			// When maxSize is too small to fit even one raw character plus the decorations,
			// the function should return the original unsplit comment.
			// For example, if sepEnd = "ZZ" (length 2) and sepStart = "TOP" (length 3),
			// then we require at least maxSize >= 2+1 = 3 for first chunk
			// and maxSize >= 3+1 = 4 for subsequent chunks.
			// Here we set maxSize to 5 (which is borderline) and expect unsplit output.
			maxSize:  5,
			sepEnd:   "ZZ",
			sepStart: "TOP",
			want:     []string{"Hello, world!"},
		},
		{
			name:    "MaxSizeTooSmall_UnsplitFallback",
			comment: "abc",
			// sepEnd="YYZ" => length=3 => we need at least 4 to store 1 raw char + suffix
			// maxSize=2 => triggers the fallback to unsplit.
			maxSize:  2,
			sepEnd:   "YYZ",
			sepStart: "S",
			want:     []string{"abc"},
		},
		{
			name:     "NewlinesInComment", // Test with newlines to verify they are preserved.
			comment:  "line1\nline2\nline3",
			maxSize:  20, // Comment fits unsplit.
			sepEnd:   "--E--",
			sepStart: "--S--",
			want:     []string{"line1\nline2\nline3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitComment(tt.comment, tt.maxSize, tt.sepEnd, tt.sepStart)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("%s:\n got: %#v\nwant: %#v", tt.name, got, tt.want)
			}
		})
	}
}

// TestSplitComment_RealSeparators uses real production separator values and maximum comment length.
// This integration-style test verifies that with the actual production values:
// - No chunk exceeds MaxCommentLength.
// - The first chunk does not have the prefix (sepStart).
// - The final chunk does not have the suffix (sepEnd).
func TestSplitComment_RealSeparators(t *testing.T) {
	// Create a long comment that exceeds MaxCommentLength.
	comment := strings.Repeat("a", MaxCommentLength+100)
	got := splitComment(comment, MaxCommentLength, sepEnd, sepStart)
	// Verify that none of the chunks exceed MaxCommentLength.
	for i, part := range got {
		if len(part) > MaxCommentLength {
			t.Errorf("Chunk %d exceeds MaxCommentLength: len=%d", i, len(part))
		}
	}
	// Verify that the first chunk does not have the sepStart prefix.
	if len(got) > 0 && strings.HasPrefix(got[0], sepStart) {
		t.Errorf("First chunk should not start with sepStart")
	}
	// Verify that the final chunk does not have the sepEnd suffix.
	if len(got) > 0 && strings.HasSuffix(got[len(got)-1], sepEnd) {
		t.Errorf("Final chunk should not end with sepEnd")
	}
}
