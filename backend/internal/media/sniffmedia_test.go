package media

import "testing"

func TestSniffMediaDetectsVideo(t *testing.T) {
	cases := []struct {
		name string
		body []byte
		want string
	}{
		{
			name: "mp4_isom",
			body: append([]byte{0x00, 0x00, 0x00, 0x18}, []byte("ftypisom")...),
			want: "video/mp4",
		},
		{
			name: "mp4_mp42",
			body: append([]byte{0x00, 0x00, 0x00, 0x18}, []byte("ftypmp42")...),
			want: "video/mp4",
		},
		{
			name: "quicktime",
			body: append([]byte{0x00, 0x00, 0x00, 0x14}, []byte("ftypqt  ")...),
			want: "video/quicktime",
		},
		{
			name: "webm",
			body: []byte{0x1A, 0x45, 0xDF, 0xA3, 0x01, 0x00, 0x00, 0x00},
			want: "video/webm",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mime, ok := SniffMedia(tc.body)
			if !ok || mime != tc.want {
				t.Fatalf("SniffMedia(%s) = (%q,%v), want (%q,true)", tc.name, mime, ok, tc.want)
			}
		})
	}
}

func TestSniffMediaStillPassesImages(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if mime, ok := SniffMedia(png); !ok || mime != "image/png" {
		t.Fatalf("SniffMedia(png) = (%q,%v), want image/png", mime, ok)
	}
	gif := []byte("GIF89a...")
	if mime, ok := SniffMedia(gif); !ok || mime != "image/gif" {
		t.Fatalf("SniffMedia(gif) = (%q,%v), want image/gif", mime, ok)
	}
}

func TestSniffMediaRejectsAvifAsImageNotVideo(t *testing.T) {
	// AVIF carries ftyp but must stay an image, not fall into the video branch.
	avif := append([]byte{0x00, 0x00, 0x00, 0x1c}, []byte("ftypavif")...)
	if mime, ok := SniffMedia(avif); !ok || mime != "image/avif" {
		t.Fatalf("SniffMedia(avif) = (%q,%v), want image/avif", mime, ok)
	}
}
