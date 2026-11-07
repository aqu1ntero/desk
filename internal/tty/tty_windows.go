package tty

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// PTY is a program in a ConPTY (Windows 10 1809 and later): in and out are
// our ends of the pseudo console's pipes.
type PTY struct {
	in, out *os.File
	p       *os.Process
	st      *os.ProcessState
	done    chan struct{} // closed once the program ended (st set)

	mu  sync.Mutex
	hpc windows.Handle // 0 once closed
}

// Start runs cmd in a w×h pseudo console, as its console. cmd.Process is
// set, but cmd.Wait can't be used (the process wasn't started by exec):
// use Wait.
func Start(cmd *exec.Cmd, w, h int) (*PTY, error) {
	if cmd.Err != nil { // exec.Command's lookup failed
		return nil, cmd.Err
	}
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, err
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		windows.CloseHandle(inR)
		windows.CloseHandle(inW)
		return nil, err
	}
	p := &PTY{in: os.NewFile(uintptr(inW), "conpty-in"), out: os.NewFile(uintptr(outR), "conpty-out"), done: make(chan struct{})}
	err := windows.CreatePseudoConsole(coord(w, h), inR, outW, 0, &p.hpc)
	windows.CloseHandle(inR) // the pseudo console holds its own
	windows.CloseHandle(outW)
	if err == nil {
		err = p.spawn(cmd)
		if err != nil {
			windows.ClosePseudoConsole(p.hpc)
		}
	}
	if err != nil {
		p.in.Close()
		p.out.Close()
		return nil, err
	}
	go func() {
		p.st, _ = cmd.Process.Wait()
		close(p.done)
		// The console host outlives the program while the pseudo console is
		// open, and with it the output pipe: closing it flushes what's left
		// and ends the reader's Read, as the program's end does on Unix.
		p.closeConsole()
	}()
	return p, nil
}

// spawn starts cmd attached to the pseudo console (CreateProcess with
// PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE: os/exec has no way to pass it).
func (p *PTY) spawn(cmd *exec.Cmd) error {
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return err
	}
	defer attrs.Delete()
	// The attribute's value is the HPCON itself, not a pointer to it.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&p.hpc)), unsafe.Sizeof(p.hpc)); err != nil {
		return err
	}
	si := &windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(*si))
	// No std handles of ours: without this flag a desk started with its
	// own redirected would hand them down instead of the console's.
	si.Flags = windows.STARTF_USESTDHANDLES
	app, err := windows.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return err
	}
	line, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(cmd.Args))
	if err != nil {
		return err
	}
	var dir *uint16
	if cmd.Dir != "" {
		if dir, err = windows.UTF16PtrFromString(cmd.Dir); err != nil {
			return err
		}
	}
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	var pi windows.ProcessInformation
	err = windows.CreateProcess(app, line, nil, nil, false,
		windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT,
		envBlock(env), dir, &si.StartupInfo, &pi)
	if err != nil {
		return &os.PathError{Op: "CreateProcess", Path: cmd.Path, Err: err}
	}
	defer windows.CloseHandle(pi.Process)
	windows.CloseHandle(pi.Thread)
	// FindProcess opens its own handle while ours keeps the pid from reuse.
	if cmd.Process, err = os.FindProcess(int(pi.ProcessId)); err != nil {
		windows.TerminateProcess(pi.Process, 1)
	}
	return err
}

// envBlock is env as CreateProcess takes it: NUL-terminated strings, then
// one more NUL. A variable set twice keeps its last value (as on Unix; the
// block's lookup would take the first), names compared ignoring case.
func envBlock(env []string) *uint16 {
	seen := map[string]int{}
	var kept []string
	for _, kv := range env {
		if kv == "" {
			continue
		}
		k := strings.ToUpper(kv)
		if i := strings.IndexByte(kv[1:], '='); i >= 0 { // from 1: "=C:=C:\x" names hold a '='
			k = k[:i+1]
		}
		if j, ok := seen[k]; ok {
			kept[j] = kv
			continue
		}
		seen[k] = len(kept)
		kept = append(kept, kv)
	}
	var b []uint16
	for _, kv := range kept {
		if s, err := windows.UTF16FromString(kv); err == nil { // with its NUL
			b = append(b, s...)
		}
	}
	return &append(b, 0, 0)[0]
}

func coord(w, h int) windows.Coord { return windows.Coord{X: int16(w), Y: int16(h)} }

func (p *PTY) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *PTY) Write(b []byte) (int, error) { return p.in.Write(b) }

// Resize tells the program its console is now w×h.
func (p *PTY) Resize(w, h int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hpc != 0 {
		windows.ResizePseudoConsole(p.hpc, coord(w, h))
	}
}

func (p *PTY) wait() (*os.ProcessState, error) {
	<-p.done
	return p.st, nil
}

// closeConsole closes the pseudo console once. Before Windows 11 24H2 it
// blocks until the output is drained, which the reader does meanwhile.
func (p *PTY) closeConsole() {
	p.mu.Lock()
	h := p.hpc
	p.hpc = 0
	p.mu.Unlock()
	if h != 0 {
		windows.ClosePseudoConsole(h)
	}
}

// Hangup closes the pseudo console, which ends the programs attached to it
// (CTRL_CLOSE_EVENT, then terminated: SIGHUP's part), then our pipe ends.
// In the background, since closing may wait on the programs.
func (p *PTY) Hangup() {
	go func() {
		p.closeConsole()
		p.in.Close()
		p.out.Close()
	}()
}
