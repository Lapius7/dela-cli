package main

import (
	"fmt"
	"sync"
	"time"
)

// spinnerは接続待ちの間だけ表示する簡易プログレス表示。TTYでなければ
// 静的な1行だけ出す(パイプ/リダイレクト時にちらつく制御文字が混ざらないようにするため。
// sca-cliのspinner.goと同じ考え方)。
type spinner struct {
	label  string
	stopCh chan struct{}
	wg     sync.WaitGroup
}

var spinnerFrames = []string{"⠠⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func newSpinner(label string) *spinner {
	return &spinner{label: label, stopCh: make(chan struct{})}
}

func (s *spinner) start() {
	if !colorEnabled {
		fmt.Print(cyan("→") + " " + s.label + "...\r\n")
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-s.stopCh:
				return
			case <-ticker.C:
				fmt.Printf("\r\x1b[2K%s %s...", cyan(spinnerFrames[i%len(spinnerFrames)]), s.label)
				i++
			}
		}
	}()
}

// stopはスピナーを止め、行をクリアする(呼び出し側が続けて確定行を出す)
func (s *spinner) stop() {
	if !colorEnabled {
		return
	}
	close(s.stopCh)
	s.wg.Wait()
	fmt.Print("\r\x1b[2K")
}
