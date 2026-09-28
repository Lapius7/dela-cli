package main

import (
	"fmt"
	"sync"
	"time"
)

// spinnerは接続待ちの間だけ表示する簡易プログレス表示。TTYでなければ
// 静的な1行だけ出す(パイプ/リダイレクト時にちらつく制御文字が混ざらないようにするため。
// sca-cliのspinner.goと同じ考え方)。
//
// start/stopは同じインスタンスに対して何度呼んでも安全(runが想定外の出力を
// 見つけた際に一旦stopして生ログを出し、再度startして待機を続けるため)。
// startのたびにstopChとstopOnceを作り直すことで、「stop→start→stop」の
// 2回目のstopが、既に閉じたチャンネルではなく新しい世代のチャンネルに対して
// 効くようにしている(そうしないと2周目のspinnerが起動直後に既に閉じたチャンネルへ
// マッチして即終了してしまう)。
type spinner struct {
	label    string
	mu       sync.Mutex
	stopCh   chan struct{}
	stopOnce *sync.Once
	wg       sync.WaitGroup
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func newSpinner(label string) *spinner {
	return &spinner{label: label}
}

func (s *spinner) start() {
	if !colorEnabled {
		fmt.Print(cyan("→") + " " + s.label + "...\r\n")
		return
	}
	s.mu.Lock()
	stopCh := make(chan struct{})
	s.stopCh = stopCh
	s.stopOnce = &sync.Once{}
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				fmt.Printf("\r\x1b[2K%s %s...", cyan(spinnerFrames[i%len(spinnerFrames)]), s.label)
				i++
			}
		}
	}()
}

// stopは現在動いているスピナーを止め、行をクリアする(呼び出し側が続けて確定行を出す)
func (s *spinner) stop() {
	if !colorEnabled {
		return
	}
	s.mu.Lock()
	stopCh, once := s.stopCh, s.stopOnce
	s.mu.Unlock()
	if stopCh == nil || once == nil {
		return
	}
	once.Do(func() {
		close(stopCh)
	})
	s.wg.Wait()
	fmt.Print("\r\x1b[2K")
}
