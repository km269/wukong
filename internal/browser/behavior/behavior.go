// Package behavior provides human-like behavior simulation for browser automation.
// It includes mouse movement, scrolling, typing, and other human interactions.
package behavior

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"time"
)

// Config holds configuration for behavior simulation.
type Config struct {
	MouseMoveSpeed      float64 // Pixels per second (default: 800)
	TypingSpeedMinSpeed float64 // Characters per second (default: 5)
	ScrollSpeed         float64 // Scroll speed (default: 800)
}

// Simulator manages human-like behavior simulation.
type Simulator struct {
	config Config
	rng    *rand.Rand
}

// New creates a new behavior simulator.
func New(config Config) *Simulator {
	if config.MouseMoveSpeed <= 0 {
		config.MouseMoveSpeed = 800
	}
	if config.TypingSpeedMinSpeed <= 0 {
		config.TypingSpeedMinSpeed = 5
	}
	if config.ScrollSpeed <= 0 {
		config.ScrollSpeed = 800
	}
	return &Simulator{
		config: config,
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// DefaultConfig returns the default behavior configuration.
func DefaultConfig() Config {
	return Config{
		MouseMoveSpeed:      800,
		TypingSpeedMinSpeed: 5,
		ScrollSpeed:         800,
	}
}

// Point represents a 2D coordinate.
type Point struct {
	X float64
	Y float64
}

// MouseMove generates a human-like mouse movement path from start to end.
// Uses Bezier curve to generate smooth, human-like movement.
// Returns a slice of points representing the movement path.
func (s *Simulator) MouseMove(start, end Point, steps int) []Point {
	if steps <= 0 {
		distance := math.Hypot(end.X-start.X, end.Y-start.Y)
		steps = int(math.Max(10, distance/20))
	}

	// Generate random control points for Bezier curve (once per path)
	cp1 := Point{
		X: start.X + (end.X-start.X)*0.3 + (s.rng.Float64()*0.4-0.2)*(end.X-start.X),
		Y: start.Y + (end.Y-start.Y)*0.3 + (s.rng.Float64()*0.4-0.2)*(end.Y-start.Y),
	}
	cp2 := Point{
		X: start.X + (end.X-start.X)*0.7 + (s.rng.Float64()*0.4-0.2)*(end.X-start.X),
		Y: start.Y + (end.Y-start.Y)*0.7 + (s.rng.Float64()*0.4-0.2)*(end.Y-start.Y),
	}

	points := make([]Point, steps)
	for i := 0; i < steps; i++ {
		t := float64(i) / float64(steps-1)

		// Cubic Bezier interpolation
		oneMinusT := 1 - t
		points[i] = Point{
			X: math.Pow(oneMinusT, 3)*start.X +
				3*math.Pow(oneMinusT, 2)*t*cp1.X +
				3*oneMinusT*math.Pow(t, 2)*cp2.X +
				math.Pow(t, 3)*end.X,
			Y: math.Pow(oneMinusT, 3)*start.Y +
				3*math.Pow(oneMinusT, 2)*t*cp1.Y +
				3*oneMinusT*math.Pow(t, 2)*cp2.Y +
				math.Pow(t, 3)*end.Y,
		}
	}

	return points
}

// MouseMoveDuration calculates the duration needed to move from start to end.
func (s *Simulator) MouseMoveDuration(start, end Point) time.Duration {
	distance := math.Hypot(end.X-start.X, end.Y-start.Y)
	baseDuration := time.Duration(distance/s.config.MouseMoveSpeed*1000) * time.Millisecond
	// Add random variation (±20%)
	variation := time.Duration((s.rng.Float64()*0.4 - 0.2) * float64(baseDuration))
	return baseDuration + variation
}

// ScrollDuration calculates duration to scroll from startY to endY.
func (s *Simulator) ScrollDuration(startY, endY float64) time.Duration {
	distance := math.Abs(endY - startY)
	baseDuration := time.Duration(distance/s.config.ScrollSpeed*1000) * time.Millisecond
	variation := time.Duration((s.rng.Float64()*0.4 - 0.2) * float64(baseDuration))
	return baseDuration + variation
}

// TypingDelay returns a random delay between keystrokes.
func (s *Simulator) TypingDelay() time.Duration {
	baseDelay := time.Duration(1000/s.config.TypingSpeedMinSpeed) * time.Millisecond
	variation := time.Duration(s.rng.Float64() * 1.5 * float64(baseDelay))
	return baseDelay + variation
}

// RandomPause returns a random pause duration for natural pauses.
func (s *Simulator) RandomPause(min, max time.Duration) time.Duration {
	if min <= 0 {
		min = 100 * time.Millisecond
	}
	if max <= min {
		max = min * 2
	}
	return min + time.Duration(s.rng.Float64()*float64(max-min))
}

// BrowserController defines the interface for browser interaction.
type BrowserController interface {
	// MoveMouse moves the mouse to the specified coordinates.
	MoveMouse(ctx context.Context, x, y float64) error
	// Scroll scrolls the page by the specified delta.
	Scroll(ctx context.Context, deltaX, deltaY float64) error
	// TypeText types text with human-like delays.
	TypeText(ctx context.Context, text string) error
	// Click clicks at the current mouse position.
	Click(ctx context.Context) error
}

// SimulateNaturalNavigation simulates natural navigation behavior before interacting with a page.
func (s *Simulator) SimulateNaturalNavigation(ctx context.Context, browser BrowserController) error {
	// Random pause before starting
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(s.RandomPause(500*time.Millisecond, 2*time.Second)):
	}

	// Random scroll
	scrollDelta := float64(s.rng.Intn(200) - 100)
	if err := browser.Scroll(ctx, 0, scrollDelta); err != nil {
		return fmt.Errorf("scroll: %w", err)
	}

	// Random pause
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(s.RandomPause(300*time.Millisecond, 1*time.Second)):
	}

	return nil
}

// SimulateTyping simulates human-like typing.
func (s *Simulator) SimulateTyping(ctx context.Context, browser BrowserController, text string) error {
	for i, char := range text {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := browser.TypeText(ctx, string(char)); err != nil {
			return fmt.Errorf("type character: %w", err)
		}

		// Random delay between keystrokes
		delay := s.TypingDelay()
		// Longer delay occasionally for natural pauses
		if i > 0 && (i%5 == 0 || char == ' ' || s.rng.Float64() < 0.05) {
			delay += s.RandomPause(100*time.Millisecond, 500*time.Millisecond)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return nil
}

// SimulateClick simulates a human-like click with pre and post movement.
func (s *Simulator) SimulateClick(ctx context.Context, browser BrowserController, target Point) error {
	start := Point{float64(s.rng.Intn(200)), float64(s.rng.Intn(200))}
	path := s.MouseMove(start, target, 0)
	duration := s.MouseMoveDuration(start, target)
	stepDuration := duration / time.Duration(len(path))

	for _, p := range path {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := browser.MoveMouse(ctx, p.X, p.Y); err != nil {
			return fmt.Errorf("move mouse: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(stepDuration):
		}
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(s.RandomPause(100*time.Millisecond, 300*time.Millisecond)):
	}

	if err := browser.Click(ctx); err != nil {
		return fmt.Errorf("click: %w", err)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(s.RandomPause(200*time.Millisecond, 500*time.Millisecond)):
	}

	return nil
}

// SimulateRandomHover moves to a random position and hovers briefly.
func (s *Simulator) SimulateRandomHover(ctx context.Context, browser BrowserController, viewportWidth, viewportHeight float64) error {
	target := Point{
		X: s.rng.Float64()*viewportWidth*0.8 + viewportWidth*0.1,
		Y: s.rng.Float64()*viewportHeight*0.8 + viewportHeight*0.1,
	}

	path := s.MouseMove(Point{X: 0, Y: 0}, target, 0)
	duration := s.MouseMoveDuration(Point{X: 0, Y: 0}, target)
	stepDuration := duration / time.Duration(len(path))

	for _, p := range path {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := browser.MoveMouse(ctx, p.X, p.Y); err != nil {
			return fmt.Errorf("hover move: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(stepDuration):
		}
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(s.RandomPause(200*time.Millisecond, 800*time.Millisecond)):
	}

	return nil
}

// SimulatePageExploration performs random scrolls and hover patterns
// to mimic a human exploring a page before interacting.
func (s *Simulator) SimulatePageExploration(ctx context.Context, browser BrowserController, viewportHeight float64) error {
	scrollCount := 1 + s.rng.Intn(3)
	for i := 0; i < scrollCount; i++ {
		scrollDelta := float64(s.rng.Intn(300) - 50)
		scrollDuration := s.ScrollDuration(0, scrollDelta)
		if err := browser.Scroll(ctx, 0, scrollDelta); err != nil {
			return fmt.Errorf("scroll %d: %w", i, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(scrollDuration):
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.RandomPause(200*time.Millisecond, 600*time.Millisecond)):
		}
	}

	hoverCount := s.rng.Intn(2)
	for i := 0; i < hoverCount; i++ {
		if err := s.SimulateRandomHover(ctx, browser, 1920, viewportHeight); err != nil {
			return err
		}
	}

	return nil
}

// SimulateReadingPause simulates a pause as if the user is reading content.
func (s *Simulator) SimulateReadingPause(ctx context.Context) error {
	duration := s.RandomPause(1*time.Second, 4*time.Second)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(duration):
	}
	return nil
}
