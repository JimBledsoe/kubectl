package test

import (
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
)

// Etcd knows how to run an etcd server.
//
// The documentation and examples for the Etcd's properties can be found in
// in the documentation for the `APIServer`, as both implement a `ControlPaneProcess`.
type Etcd struct {
	AddressManager AddressManager
	Path           string
	ProcessStarter SimpleSessionStarter
	DataDirManager DataDirManager
	StopTimeout    time.Duration
	StartTimeout   time.Duration
	session        SimpleSession
	stdOut         *gbytes.Buffer
	stdErr         *gbytes.Buffer
}

// DataDirManager knows how to manage a data directory to be used by Etcd.
type DataDirManager interface {
	Create() (string, error)
	Destroy() error
}

//go:generate counterfeiter . DataDirManager

// SimpleSession describes a CLI session. You can get output, the exit code, and you can terminate it.
//
// It is implemented by *gexec.Session.
type SimpleSession interface {
	Buffer() *gbytes.Buffer
	ExitCode() int
	Terminate() *gexec.Session
}

//go:generate counterfeiter . SimpleSession

// SimpleSessionStarter knows how to start a exec.Cmd with a writer for both StdOut & StdErr and returning it wrapped
// in a `SimpleSession`.
type SimpleSessionStarter func(command *exec.Cmd, out, err io.Writer) (SimpleSession, error)

// URL returns the URL Etcd is listening on. Clients can use this to connect to Etcd.
func (e *Etcd) URL() (string, error) {
	if e.AddressManager == nil {
		return "", fmt.Errorf("Etcd's AddressManager is not initialized")
	}
	port, err := e.AddressManager.Port()
	if err != nil {
		return "", err
	}
	host, err := e.AddressManager.Host()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("http://%s:%d", host, port), nil
}

// Start starts the etcd, waits for it to come up, and returns an error, if occoured.
func (e *Etcd) Start() error {
	e.ensureInitialized()

	port, host, err := e.AddressManager.Initialize()
	if err != nil {
		return err
	}

	dataDir, err := e.DataDirManager.Create()
	if err != nil {
		return err
	}

	clientURL := fmt.Sprintf("http://%s:%d", host, port)
	args := []string{
		"--debug",
		"--listen-peer-urls=http://localhost:0",
		fmt.Sprintf("--advertise-client-urls=%s", clientURL),
		fmt.Sprintf("--listen-client-urls=%s", clientURL),
		fmt.Sprintf("--data-dir=%s", dataDir),
	}

	detectedStart := e.stdErr.Detect(fmt.Sprintf(
		"serving insecure client requests on %s", host))
	timedOut := time.After(e.StartTimeout)

	command := exec.Command(e.Path, args...)
	e.session, err = e.ProcessStarter(command, e.stdOut, e.stdErr)
	if err != nil {
		return err
	}

	select {
	case <-detectedStart:
		return nil
	case <-timedOut:
		return fmt.Errorf("timeout waiting for etcd to start serving")
	}
}

func (e *Etcd) ensureInitialized() {
	if e.Path == "" {
		e.Path = DefaultBinPathFinder("etcd")
	}

	if e.AddressManager == nil {
		e.AddressManager = &DefaultAddressManager{}
	}
	if e.ProcessStarter == nil {
		e.ProcessStarter = func(command *exec.Cmd, out, err io.Writer) (SimpleSession, error) {
			return gexec.Start(command, out, err)
		}
	}
	if e.DataDirManager == nil {
		e.DataDirManager = NewTempDirManager()
	}
	if e.StopTimeout == 0 {
		e.StopTimeout = 20 * time.Second
	}
	if e.StartTimeout == 0 {
		e.StartTimeout = 20 * time.Second
	}

	e.stdOut = gbytes.NewBuffer()
	e.stdErr = gbytes.NewBuffer()
}

// Stop stops this process gracefully, waits for its termination, and cleans up the data directory.
func (e *Etcd) Stop() error {
	if e.session == nil {
		return nil
	}

	session := e.session.Terminate()
	detectedStop := session.Exited
	timedOut := time.After(e.StopTimeout)

	select {
	case <-detectedStop:
		break
	case <-timedOut:
		return fmt.Errorf("timeout waiting for etcd to stop")
	}

	return e.DataDirManager.Destroy()
}

// ExitCode returns the exit code of the process, if it has exited. If it hasn't exited yet, ExitCode returns -1.
func (e *Etcd) ExitCode() int {
	return e.session.ExitCode()
}

// Buffer implements the gbytes.BufferProvider interface and returns the stdout of the process
func (e *Etcd) Buffer() *gbytes.Buffer {
	return e.session.Buffer()
}
