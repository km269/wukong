// Package builtin provides the Auto Visualiser extension.
// It automatically generates charts and visualisations in conversations.
package builtin

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/km269/wukong/internal/config"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	degToRad = math.Pi / 180.0
)

// VisualiserToolSet provides tools for auto-generating charts
// and visualisations during agent conversations.
type VisualiserToolSet struct {
	tools  []tool.Tool
	cfg    *config.WukongConfig
	inited bool
	closed bool
}

// NewVisualiserToolSet creates the auto visualiser tool set.
func NewVisualiserToolSet(cfg *config.WukongConfig) *VisualiserToolSet {
	ts := &VisualiserToolSet{cfg: cfg}
	ts.tools = []tool.Tool{
		function.NewFunctionTool(
			ts.generateChart,
			function.WithName("visualiser_chart"),
			function.WithDescription(
				"Generate a chart or diagram from data. "+
					"Supports bar, line, pie, scatter, and flow "+
					"chart types. Returns an SVG file path.",
			),
		),
		function.NewFunctionTool(
			ts.generateDiagram,
			function.WithName("visualiser_diagram"),
			function.WithDescription(
				"Generate a diagram (flowchart, sequence, "+
					"architecture, ER, class diagram). Uses "+
					"Mermaid-compatible syntax. Returns SVG path.",
			),
		),
		function.NewFunctionTool(
			ts.generateTable,
			function.WithName("visualiser_table"),
			function.WithDescription(
				"Generate a formatted table from data. Returns "+
					"an HTML table file path.",
			),
		),
	}
	return ts
}

// Tools returns the visualiser tools.
func (ts *VisualiserToolSet) Tools(ctx context.Context) []tool.Tool {
	return ts.tools
}

// Name returns the tool set name.
func (ts *VisualiserToolSet) Name() string {
	return "auto_visualiser"
}

// Init initializes the tool set.
func (ts *VisualiserToolSet) Init(ctx context.Context) error {
	outputDir := ts.cfg.Visualiser.OutputDir
	if outputDir == "" {
		outputDir = ".wukong_visuals"
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create visuals dir: %w", err)
	}
	ts.inited = true
	return nil
}

// Close releases resources.
func (ts *VisualiserToolSet) Close() error {
	ts.closed = true
	return nil
}

// ChartType enumerates supported chart types.
type ChartType string

const (
	ChartBar     ChartType = "bar"
	ChartLine    ChartType = "line"
	ChartPie     ChartType = "pie"
	ChartScatter ChartType = "scatter"
	ChartFlow    ChartType = "flow"
)

// ChartReq is the input for generating a chart.
type ChartReq struct {
	Type   string    `json:"type" jsonschema:"description=Chart type: bar, line, pie, scatter, flow"`
	Title  string    `json:"title" jsonschema:"description=Chart title"`
	Labels []string  `json:"labels" jsonschema:"description=X-axis labels or categories"`
	Values []float64 `json:"values" jsonschema:"description=Data values"`
	Width  int       `json:"width,omitempty" jsonschema:"description=Chart width in pixels"`
	Height int       `json:"height,omitempty" jsonschema:"description=Chart height in pixels"`
}

// ChartRsp is the output for chart generation.
type ChartRsp struct {
	Success  bool   `json:"success"`
	FilePath string `json:"file_path,omitempty"`
	Message  string `json:"message,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (ts *VisualiserToolSet) generateChart(
	ctx context.Context, req ChartReq,
) (ChartRsp, error) {
	outputDir := ts.cfg.Visualiser.OutputDir
	if outputDir == "" {
		outputDir = ".wukong_visuals"
	}

	width := req.Width
	if width <= 0 || width > ts.cfg.Visualiser.MaxWidth {
		width = 800
	}
	height := req.Height
	if height <= 0 || height > ts.cfg.Visualiser.MaxHeight {
		height = 400
	}

	svg := generateChartSVG(
		req.Type, req.Title, req.Labels, req.Values,
		width, height,
	)

	filename := fmt.Sprintf(
		"chart_%s_%s.svg",
		req.Type, sanitizeFilename(req.Title),
	)
	filePath := filepath.Join(outputDir, filename)

	if err := os.WriteFile(filePath, []byte(svg), 0644); err != nil {
		return ChartRsp{
			Success: false,
			Error:   fmt.Sprintf("write SVG: %v", err),
		}, nil
	}

	return ChartRsp{
		Success:  true,
		FilePath: filePath,
		Message:  fmt.Sprintf("Chart saved: %s (%dx%d)", filename, width, height),
	}, nil
}

// DiagramReq is the input for generating a diagram.
type DiagramReq struct {
	Type        string `json:"type" jsonschema:"description=Diagram type: flowchart, sequence, architecture, er, class"`
	Title       string `json:"title" jsonschema:"description=Diagram title"`
	Description string `json:"description" jsonschema:"description=Text description of the diagram to generate"`
}

// DiagramRsp is the output for diagram generation.
type DiagramRsp struct {
	Success  bool   `json:"success"`
	FilePath string `json:"file_path,omitempty"`
	Message  string `json:"message,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (ts *VisualiserToolSet) generateDiagram(
	ctx context.Context, req DiagramReq,
) (DiagramRsp, error) {
	outputDir := ts.cfg.Visualiser.OutputDir
	if outputDir == "" {
		outputDir = ".wukong_visuals"
	}

	// Generate a Mermaid-compatible diagram description
	mermaidCode := generateMermaidDiagram(
		req.Type, req.Title, req.Description,
	)

	filename := fmt.Sprintf(
		"diagram_%s_%s.mmd",
		req.Type, sanitizeFilename(req.Title),
	)
	filePath := filepath.Join(outputDir, filename)

	// Add HTML wrapper with Mermaid for viewing
	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>%s</title>
  <script src="https://cdn.jsdelivr.net/npm/mermaid/dist/mermaid.min.js"></script>
  <script>mermaid.initialize({startOnLoad:true, theme:'default'});</script>
</head>
<body>
  <h2>%s (%s Diagram)</h2>
  <div class="mermaid">
%s
  </div>
</body>
</html>`, req.Title, req.Title, req.Type, mermaidCode)

	htmlPath := strings.TrimSuffix(filePath, ".mmd") + ".html"
	if err := os.WriteFile(htmlPath, []byte(html), 0644); err != nil {
		return DiagramRsp{
			Success: false,
			Error:   fmt.Sprintf("write HTML: %v", err),
		}, nil
	}

	return DiagramRsp{
		Success:  true,
		FilePath: htmlPath,
		Message:  fmt.Sprintf("Diagram saved: %s", filepath.Base(htmlPath)),
	}, nil
}

// TableReq is the input for generating a table.
type TableReq struct {
	Title   string     `json:"title" jsonschema:"description=Table title"`
	Headers []string   `json:"headers" jsonschema:"description=Column headers"`
	Rows    [][]string `json:"rows" jsonschema:"description=Table data rows"`
}

// TableRsp is the output for table generation.
type TableRsp struct {
	Success  bool   `json:"success"`
	FilePath string `json:"file_path,omitempty"`
	Message  string `json:"message,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (ts *VisualiserToolSet) generateTable(
	ctx context.Context, req TableReq,
) (TableRsp, error) {
	outputDir := ts.cfg.Visualiser.OutputDir
	if outputDir == "" {
		outputDir = ".wukong_visuals"
	}

	html := generateTableHTML(req.Title, req.Headers, req.Rows)

	filename := fmt.Sprintf(
		"table_%s.html",
		sanitizeFilename(req.Title),
	)
	filePath := filepath.Join(outputDir, filename)

	if err := os.WriteFile(filePath, []byte(html), 0644); err != nil {
		return TableRsp{
			Success: false,
			Error:   fmt.Sprintf("write HTML: %v", err),
		}, nil
	}

	return TableRsp{
		Success:  true,
		FilePath: filePath,
		Message:  fmt.Sprintf("Table saved: %s", filename),
	}, nil
}

// generateChartSVG generates a simple SVG chart.
// labels and values must have the same length for pie/bar charts;
// if they differ, labels are truncated or padded to match values.
func generateChartSVG(
	chartType, title string, labels []string,
	values []float64, width, height int,
) string {
	if len(values) == 0 {
		return fmt.Sprintf(
			`<svg width="%d" height="%d" xmlns="http://www.w3.org/2000/svg">
  <text x="%d" y="%d" text-anchor="middle" font-size="16">No data</text>
</svg>`, width, height, width/2, height/2)
	}

	// Ensure labels match values length
	if len(labels) < len(values) {
		// Pad with numbered labels
		for i := len(labels); i < len(values); i++ {
			labels = append(labels, fmt.Sprintf("Item %d", i+1))
		}
	} else if len(labels) > len(values) {
		labels = labels[:len(values)]
	}

	// Find max value for scaling
	maxVal := values[0]
	for _, v := range values {
		if v > maxVal {
			maxVal = v
		}
	}
	if maxVal == 0 {
		maxVal = 1
	}

	margin := 60
	chartW := width - 2*margin
	chartH := height - 2*margin
	barW := chartW / len(values)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		`<svg width="%d" height="%d" xmlns="http://www.w3.org/2000/svg">
  <style>
    .bar { fill: #4A90D9; }
    .bar:hover { fill: #357ABD; }
    .label { font-size: 12px; fill: #333; text-anchor: middle; }
    .title { font-size: 18px; fill: #222; text-anchor: middle; font-weight: bold; }
    .axis { stroke: #ccc; stroke-width: 1; }
  </style>
  <text x="%d" y="25" class="title">%s</text>
  <line x1="%d" y1="%d" x2="%d" y2="%d" class="axis"/>
  <line x1="%d" y1="%d" x2="%d" y2="%d" class="axis"/>
`, width, height, width/2, title,
		margin, margin, margin, margin+chartH,
		margin, margin+chartH, margin+chartW, margin+chartH))

	switch chartType {
	case "bar":
		for i, v := range values {
			barH := int(float64(chartH) * v / maxVal)
			x := margin + i*barW + barW/4
			y := margin + chartH - barH
			sb.WriteString(fmt.Sprintf(
				`  <rect x="%d" y="%d" width="%d" height="%d" class="bar" rx="2"/>
  <text x="%d" y="%d" class="label">%s</text>
  <text x="%d" y="%d" class="label">%.1f</text>
`,
				x, y, barW/2, barH,
				x+barW/4, margin+chartH+20, labels[i],
				x+barW/4, y-5, v))
		}
	case "line":
		points := make([]string, len(values))
		for i, v := range values {
			x := margin + i*barW + barW/2
			y := margin + chartH - int(float64(chartH)*v/maxVal)
			points[i] = fmt.Sprintf("%d,%d", x, y)
		}
		sb.WriteString(fmt.Sprintf(
			`  <polyline points="%s" fill="none" stroke="#4A90D9" stroke-width="2"/>
`, strings.Join(points, " ")))
		for i, v := range values {
			x := margin + i*barW + barW/2
			y := margin + chartH - int(float64(chartH)*v/maxVal)
			sb.WriteString(fmt.Sprintf(
				`  <circle cx="%d" cy="%d" r="4" fill="#4A90D9"/>
  <text x="%d" y="%d" class="label">%s</text>
  <text x="%d" y="%d" class="label">%.1f</text>
`,
				x, y,
				x, margin+chartH+20, labels[i],
				x, y-10, v))
		}
	case "pie":
		cx := float64(width) / 2.0
		cy := float64(height) / 2.0
		r := float64(chartH) / 2.0
		if float64(chartW)/2.0 < r {
			r = float64(chartW) / 2.0
		}
		total := 0.0
		for _, v := range values {
			total += v
		}
		colors := []string{
			"#4A90D9", "#50C878", "#FF6B6B", "#FFD93D",
			"#9B59B6", "#E67E22", "#1ABC9C", "#E74C3C",
		}
		startAngle := -90.0 // Start from top
		for i, v := range values {
			sliceAngle := v / total * 360.0
			endAngle := startAngle + sliceAngle
			color := colors[i%len(colors)]

			// Compute arc path points
			startRad := startAngle * degToRad
			endRad := endAngle * degToRad

			x1 := cx + r*math.Cos(startRad)
			y1 := cy + r*math.Sin(startRad)
			x2 := cx + r*math.Cos(endRad)
			y2 := cy + r*math.Sin(endRad)

			largeArc := 0
			if sliceAngle > 180.0 {
				largeArc = 1
			}

			sb.WriteString(fmt.Sprintf(
				`  <path d="M%.1f,%.1f L%.1f,%.1f A%.1f,%.1f 0 %d,1 %.1f,%.1f Z" fill="%s" stroke="#fff" stroke-width="1"/>`+"\n",
				cx, cy, x1, y1, r, r, largeArc, x2, y2, color))

			// Label at the mid-angle of the slice
			midAngle := (startAngle + endAngle) / 2.0
			midRad := midAngle * degToRad
			labelR := r * 0.65
			lx := cx + labelR*math.Cos(midRad)
			ly := cy + labelR*math.Sin(midRad)
			pct := v / total * 100
			sb.WriteString(fmt.Sprintf(
				`  <text x="%.1f" y="%.1f" class="label" fill="#fff" font-size="11">%.0f%%</text>`+"\n",
				lx, ly+4, pct))

			startAngle = endAngle
		}
		// Legend below the pie
		for i, label := range labels {
			legendY := cy + r + 20.0 + float64(15*i)
			color := colors[i%len(colors)]
			sb.WriteString(fmt.Sprintf(
				`  <rect x="%.1f" y="%.1f" width="10" height="10" fill="%s"/>`+"\n",
				cx-r, legendY-9, color))
			sb.WriteString(fmt.Sprintf(
				`  <text x="%.1f" y="%.1f" class="label">%s: %.1f</text>`+"\n",
				cx-r+14, legendY, label, values[i]))
		}
	case "scatter":
		for i, v := range values {
			x := margin + i*barW + barW/2
			y := margin + chartH - int(float64(chartH)*v/maxVal)
			r := 5
			sb.WriteString(fmt.Sprintf(
				`  <circle cx="%d" cy="%d" r="%d" fill="#FF6B6B" opacity="0.7"/>`+"\n",
				x, y, r))
			if len(labels) > i {
				sb.WriteString(fmt.Sprintf(
					`  <text x="%d" y="%d" class="label">%s</text>`+"\n",
					x, margin+chartH+20, labels[i]))
			}
		}
	case "flow":
		// Sankey-like flow diagram with horizontal bands
		bandH := chartH / len(values)
		for i, v := range values {
			bandW := int(float64(chartW) * v / maxVal)
			if bandW < 10 {
				bandW = 10
			}
			y := margin + i*bandH + bandH/4
			colors := []string{
				"#4A90D9", "#50C878", "#FF6B6B", "#FFD93D",
				"#9B59B6", "#E67E22", "#1ABC9C", "#E74C3C",
			}
			color := colors[i%len(colors)]
			sb.WriteString(fmt.Sprintf(
				`  <rect x="%d" y="%d" width="%d" height="%d" fill="%s" rx="4" opacity="0.8"/>`+"\n",
				margin, y, bandW, bandH/2, color))
			sb.WriteString(fmt.Sprintf(
				`  <text x="%d" y="%d" class="label" text-anchor="end">%s: %.1f</text>`+"\n",
				margin+bandW-5, y+bandH/4+4, labels[i], v))
		}
	default:
		sb.WriteString(fmt.Sprintf(
			`  <text x="%d" y="%d" class="label">Chart type "%s" not fully supported yet</text>
`, width/2, height/2, chartType))
	}

	sb.WriteString("\n</svg>")
	return sb.String()
}

// generateMermaidDiagram generates Mermaid diagram syntax.
func generateMermaidDiagram(
	diagramType, title, description string,
) string {
	switch diagramType {
	case "flowchart":
		return fmt.Sprintf(
			"graph TD\n    title[%s]\n    %s",
			title, description,
		)
	case "sequence":
		return fmt.Sprintf(
			"sequenceDiagram\n    title: %s\n    %s",
			title, description,
		)
	case "architecture":
		return fmt.Sprintf(
			"graph LR\n    title[%s Architecture]\n    %s",
			title, description,
		)
	case "er":
		return fmt.Sprintf(
			"erDiagram\n    title %s\n    %s",
			title, description,
		)
	case "class":
		return fmt.Sprintf(
			"classDiagram\n    title %s\n    %s",
			title, description,
		)
	default:
		return fmt.Sprintf(
			"graph TD\n    title[%s]\n    %s",
			title, description,
		)
	}
}

// generateTableHTML generates an HTML table.
func generateTableHTML(
	title string, headers []string, rows [][]string,
) string {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>`)
	sb.WriteString(title)
	sb.WriteString(`</title>
  <style>
    body { font-family: -apple-system, sans-serif; padding: 20px; }
    table { border-collapse: collapse; width: 100%%; }
    th { background: #4A90D9; color: white; padding: 10px; text-align: left; }
    td { padding: 8px 10px; border-bottom: 1px solid #eee; }
    tr:nth-child(even) { background: #f9f9f9; }
    h1 { color: #333; }
  </style>
</head>
<body>
  <h1>`)
	sb.WriteString(title)
	sb.WriteString("</h1>\n  <table>\n    <thead><tr>")
	for _, h := range headers {
		sb.WriteString(fmt.Sprintf("<th>%s</th>", h))
	}
	sb.WriteString("</tr></thead>\n    <tbody>\n")
	for _, row := range rows {
		sb.WriteString("      <tr>")
		for _, cell := range row {
			sb.WriteString(fmt.Sprintf("<td>%s</td>", cell))
		}
		sb.WriteString("</tr>\n")
	}
	sb.WriteString("    </tbody>\n  </table>\n</body>\n</html>")
	return sb.String()
}

func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		" ", "_", "/", "_", "\\", "_",
		":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_",
		"|", "_",
	)
	s := replacer.Replace(name)
	if len(s) > 50 {
		// Truncate safely at a rune boundary to avoid breaking
		// multi-byte UTF-8 characters.
		runes := []rune(s)
		if len(runes) > 50 {
			s = string(runes[:50])
		}
	}
	return s
}
