package seccomp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/subgraph/oz"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/exec"
	"syscall"
)

const (
	STRINGARG = iota + 1
	PTRARG
	INTARG
)

type SystemCallArgs []int

func Tracer() {

	p := new(oz.Profile)
	if err := json.NewDecoder(os.Stdin).Decode(&p); err != nil {
		log.Error("unable to decode profile data: %v", err)
		os.Exit(1)
	}

	var proc_attr syscall.ProcAttr
	var sys_attr syscall.SysProcAttr

	sys_attr.Ptrace = true
	done := false
	proc_attr.Sys = &sys_attr

	cmd := os.Args[1]
	cmdArgs := os.Args[2:]
	log.Info("Tracer running command (%v) arguments (%v)\n", cmd, cmdArgs)
	c := exec.Command(cmd)
	c.SysProcAttr = &syscall.SysProcAttr{Ptrace: true}
	c.Env = os.Environ()
	c.Args = append(c.Args, cmdArgs...)

	pi, err := c.StdinPipe()
	if err != nil {
		fmt.Errorf("error creating stdin pipe for tracer process: %v", err)
		os.Exit(1)
	}
	jdata, err := json.Marshal(p)
	if err != nil {
		fmt.Errorf("Unable to marshal seccomp state: %+v", err)
		os.Exit(1)
	}
	io.Copy(pi, bytes.NewBuffer(jdata))
	log.Info(string(jdata))
	pi.Close()

	children := make(map[int]bool)

	if err := c.Start(); err == nil {
		children[c.Process.Pid] = true
		var s syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &s, syscall.WALL, nil)
		children[pid] = true
		if err != nil {
			log.Error("Error (wait4) here first: %v %i", err, pid)
		}
		log.Info("Tracing child pid: %v\n", pid)
		for done == false {
			syscall.PtraceSetOptions(pid, unix.PTRACE_O_TRACESECCOMP|unix.PTRACE_O_TRACEFORK|unix.PTRACE_O_TRACEVFORK|unix.PTRACE_O_TRACECLONE)
			syscall.PtraceCont(pid, 0)
			pid, err = syscall.Wait4(-1, &s, syscall.WALL, nil)
			if err != nil {
				log.Error("Error (wait4) here: %v %i %v\n", err, pid, children)
				if len(children) == 0 {
					done = true
				}
				continue
			}
			children[pid] = true
			if s.Exited() == true {
				delete(children, pid)
				log.Info("Child pid %v finished.\n", pid)
				if len(children) == 0 {
					done = true
				}
				continue
			}
			if s.Signaled() == true {
				log.Error("Other pid signalled %v %v", pid, s)
				delete(children, pid)
				continue
			}
			switch uint32(s) >> 8 {

			case uint32(unix.SIGTRAP) | (unix.PTRACE_EVENT_SECCOMP << 8):
				if err != nil {
					log.Error("Error (ptrace): %v", err)
					continue
				}
				var regs syscall.PtraceRegs
				err = syscall.PtraceGetRegs(pid, &regs)

				if err != nil {
					log.Error("Error (ptrace): %v", err)
				}

				systemcall, err := syscallByNum(getSyscallNumber(regs))
				if err != nil {
					log.Error("Error: %v", err)
					continue
				}

				/* TEMPORARY */

				if getSyscallNumber(regs) == 21 {
					str, err := readStringArg(pid, uintptr(regs.Rdi))
					if err == nil {
						log.Info(render_access(pid, str, int(regs.Rsi)))
					} else {
						log.Info("error %v", err)
					}

				}
				log.Info(renderSyscallBasic(pid, systemcall, regs))
				continue

			case uint32(unix.SIGTRAP) | (unix.PTRACE_EVENT_EXIT << 8):
				log.Error("Ptrace exit event detected pid %v (%s)", pid, getProcessCmdLine(pid))

			case uint32(unix.SIGTRAP) | (unix.PTRACE_EVENT_CLONE << 8):
				log.Error("Ptrace clone event detected pid %v (%s)", pid, getProcessCmdLine(pid))
				continue
			case uint32(unix.SIGTRAP) | (unix.PTRACE_EVENT_FORK << 8):
				log.Error("PTrace fork event detected pid %v (%s)", pid, getProcessCmdLine(pid))
				continue
			case uint32(unix.SIGTRAP) | (unix.PTRACE_EVENT_VFORK << 8):
				log.Error("Ptrace vfork event detected pid %v (%s)", pid, getProcessCmdLine(pid))
				continue
			case uint32(unix.SIGTRAP) | (unix.PTRACE_EVENT_VFORK_DONE << 8):
				log.Error("Ptrace vfork done event detected pid %v (%s)", pid, getProcessCmdLine(pid))
				continue
			case uint32(unix.SIGTRAP) | (unix.PTRACE_EVENT_EXEC << 8):
				log.Error("Ptrace exec event detected pid %v (%s)", pid, getProcessCmdLine(pid))
				continue
			case uint32(unix.SIGTRAP) | (unix.PTRACE_EVENT_STOP << 8):
				log.Error("Ptrace stop event detected pid %v (%s)", pid, getProcessCmdLine(pid))
				continue
			case uint32(unix.SIGTRAP):
				log.Error("SIGTRAP detected in pid %v (%s)", pid, getProcessCmdLine(pid))
				continue
			case uint32(unix.SIGCHLD):
				log.Error("SIGCHLD detected pid %v (%s)", pid, getProcessCmdLine(pid))
				continue
			case uint32(unix.SIGSTOP):
				log.Error("SIGSTOP detected pid %v (%s)", pid, getProcessCmdLine(pid))
				continue
			default:
				y := s.StopSignal()
				log.Error("Child stopped for unknown reasons pid %v status %v signal %i (%s)", pid, s, y, getProcessCmdLine(pid))
				continue
			}
		}
	}

}
