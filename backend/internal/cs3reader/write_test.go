package cs3reader

import "testing"

func TestSpaceRelative(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "space root dot", in: ".", want: ""},
		{name: "space root slash", in: "/", want: ""},
		{name: "relative with dot", in: "./Synaplan/Images", want: "Synaplan/Images"},
		{name: "absolute", in: "/Synaplan/Documents", want: "Synaplan/Documents"},
		{name: "bare", in: "Projects/Q3", want: "Projects/Q3"},
		{name: "trailing slash", in: "./Synaplan/", want: "Synaplan"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := spaceRelative(tt.in); got != tt.want {
				t.Errorf("spaceRelative(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestKindFolder(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"cat.png", "Images"},
		{"cat.AVIF", "Images"},
		{"cat.bmp", "Images"},
		{"cat.tiff", "Images"},
		{"report.pdf", "Documents"},
		{"clip.mp4", "Video"},
		{"archive.zip", ""},
	}
	for _, tt := range tests {
		if got := kindFolder(tt.filename); got != tt.want {
			t.Errorf("kindFolder(%q) = %q, want %q", tt.filename, got, tt.want)
		}
	}
}
