package pack

import (
	"testing"
)

func TestAdjustHTMLPaths(t *testing.T) {
	tests := []struct {
		name     string
		levels   int
		input    string
		expected string
	}{
		{
			name:     "single level - asset path with assets/",
			levels:   1,
			input:    `<link rel="stylesheet" href="../../../assets/style.css">`,
			expected: `<link rel="stylesheet" href="../../assets/style.css">`,
		},
		{
			name:     "single level - page link NOT adjusted",
			levels:   1,
			input:    `<a href="../../biographies-list.html">Biographies</a>`,
			expected: `<a href="../../biographies-list.html">Biographies</a>`,
		},
		{
			name:     "single level - page link with ../ NOT adjusted",
			levels:   1,
			input:    `<a href="../other/page.html">link</a>`,
			expected: `<a href="../other/page.html">link</a>`,
		},
		{
			name:     "single level - src asset path",
			levels:   1,
			input:    `<img src="../../assets/images/logo.png">`,
			expected: `<img src="../assets/images/logo.png">`,
		},
		{
			name:     "single level - src page link NOT adjusted",
			levels:   1,
			input:    `<img src="../photo.jpg">`,
			expected: `<img src="../photo.jpg">`,
		},
		{
			name:     "single level - single quoted asset",
			levels:   1,
			input:    `<link rel="stylesheet" href='../assets/style.css'>`,
			expected: `<link rel="stylesheet" href='assets/style.css'>`,
		},
		{
			name:     "single level - absolute path unchanged",
			levels:   1,
			input:    `<link rel="stylesheet" href="/assets/style.css">`,
			expected: `<link rel="stylesheet" href="/assets/style.css">`,
		},
		{
			name:     "single level - external URL unchanged",
			levels:   1,
			input:    `<img src="https://example.com/image.png">`,
			expected: `<img src="https://example.com/image.png">`,
		},
		{
			name:     "single level - no parent dir unchanged",
			levels:   1,
			input:    `<img src="assets/logo.png">`,
			expected: `<img src="assets/logo.png">`,
		},
		{
			name:   "single level - mixed page links and assets",
			levels: 1,
			input: `<html>
<head>
    <link rel="stylesheet" href="../../../assets/style.css">
</head>
<body>
    <img src="../../assets/images/logo.png">
    <a href="../other/page.html">page link</a>
    <a href="../../biographies-list.html">Biographies</a>
    <img src="../photo.jpg">
</body>
</html>`,
			expected: `<html>
<head>
    <link rel="stylesheet" href="../../assets/style.css">
</head>
<body>
    <img src="../assets/images/logo.png">
    <a href="../other/page.html">page link</a>
    <a href="../../biographies-list.html">Biographies</a>
    <img src="../photo.jpg">
</body>
</html>`,
		},
		{
			name:     "two levels - asset path stripped twice",
			levels:   2,
			input:    `<link rel="stylesheet" href="../../../assets/style.css">`,
			expected: `<link rel="stylesheet" href="../assets/style.css">`,
		},
		{
			name:     "two levels - page link NOT adjusted",
			levels:   2,
			input:    `<a href="../../biographies-list.html">Biographies</a>`,
			expected: `<a href="../../biographies-list.html">Biographies</a>`,
		},
		{
			name:   "two levels - mixed",
			levels: 2,
			input: `<html>
<head>
    <link rel="stylesheet" href="../../../assets/style.css">
</head>
<body>
    <img src="../../assets/images/logo.png">
    <a href="../../biographies-list.html">Biographies</a>
</body>
</html>`,
			expected: `<html>
<head>
    <link rel="stylesheet" href="../assets/style.css">
</head>
<body>
    <img src="assets/images/logo.png">
    <a href="../../biographies-list.html">Biographies</a>
</body>
</html>`,
		},
		{
			name:     "zero levels - no change",
			levels:   0,
			input:    `<link rel="stylesheet" href="../../../assets/style.css">`,
			expected: `<link rel="stylesheet" href="../../../assets/style.css">`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := string(adjustHTMLPaths([]byte(tt.input), tt.levels))
			if result != tt.expected {
				t.Errorf("adjustHTMLPaths(levels=%d) = \n%v\nwant \n%v", tt.levels, result, tt.expected)
			}
		})
	}
}
