// +build windows

package jobsupervisor

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/http_server"
	"golang.org/x/sys/windows/svc"

	"github.com/cloudfoundry/bosh-agent/jobsupervisor/monitor"
	"github.com/cloudfoundry/bosh-agent/jobsupervisor/winsvc"

	boshalert "github.com/cloudfoundry/bosh-agent/agent/alert"
	boshdirs "github.com/cloudfoundry/bosh-agent/settings/directories"
	bosherr "github.com/cloudfoundry/bosh-utils/errors"
	boshlog "github.com/cloudfoundry/bosh-utils/logger"
	boshsys "github.com/cloudfoundry/bosh-utils/system"
)

var pipeExePath = "C:\\var\\vcap\\bosh\\bin\\pipe.exe"

const (
	serviceDescription = "vcap"

	serviceWrapperExeFileName       = "job-service-wrapper.exe"
	serviceWrapperConfigFileName    = "job-service-wrapper.xml"
	serviceWrapperAppConfigFileName = "job-service-wrapper.exe.config"
	serviceWrapperEventJSONFileName = "job-service-wrapper.wrapper.log.json"

	serviceWrapperAppConfigBody = `
<configuration>
  <startup>
    <supportedRuntime version="v4.0" />
  </startup>
</configuration>
`
)

type serviceLogMode struct {
	Mode          string `xml:"mode,attr"`
	SizeThreshold string `xml:"sizeThreshold"`
	KeepFiles     string `xml:"keepFiles"`
}

type serviceOnfailure struct {
	Action string `xml:"action,attr"`
	Delay  string `xml:"delay,attr"`
}

type serviceEnv struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type WindowsServiceWrapperConfig struct {
	XMLName     xml.Name `xml:"service"`
	ID          string   `xml:"id"`
	Name        string   `xml:"name"`
	Description string   `xml:"description"`

	// Start exe and args if no stop arguments are provided
	Executable string   `xml:"executable"`
	Arguments  []string `xml:"argument"`

	// Optional stop arguments
	StopExecutable string   `xml:"stopexecutable,omitempty"`
	StopArguments  []string `xml:"stopargument"`

	// Replaces Arguments if stop arguments are provided
	StartArguments []string `xml:"startargument"`

	LogPath                string           `xml:"logpath"`
	LogMode                serviceLogMode   `xml:"log"`
	Onfailure              serviceOnfailure `xml:"onfailure"`
	Env                    []serviceEnv     `xml:"env,omitempty"`
	StopParentProcessFirst bool             `xml:"stopparentprocessfirst,omitempty"`
}

type StopCommand struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

type WindowsProcess struct {
	Name       string            `json:"name"`
	Executable string            `json:"executable"`
	Args       []string          `json:"args"`
	Env        map[string]string `json:"env"`
	Stop       *StopCommand      `json:"stop,omitempty"`
}

func (p *WindowsProcess) ServiceWrapperConfig(logPath string, eventPort int, machineIP string) *WindowsServiceWrapperConfig {
	args := append([]string{p.Executable}, p.Args...)
	srcv := &WindowsServiceWrapperConfig{
		ID:          p.Name,
		Name:        p.Name,
		Description: serviceDescription,
		Executable:  pipeExePath,
		LogPath:     logPath,
		LogMode: serviceLogMode{
			Mode:          "roll-by-size",
			SizeThreshold: "50000",
			KeepFiles:     "7",
		},
		Onfailure: serviceOnfailure{
			Action: "restart",
			Delay:  "5 sec",
		},
		StopParentProcessFirst: false,
	}

	// If stop args are provided the 'arguments' element
	// must be named 'startarguments'.
	if p.Stop != nil && len(p.Stop.Args) != 0 {
		srcv.StartArguments = args
		srcv.StopArguments = p.Stop.Args
		if p.Stop.Executable != "" {
			srcv.StopExecutable = p.Stop.Executable
		} else {
			srcv.StopExecutable = p.Executable // Do not use pipe
		}
	} else {
		srcv.Arguments = args
	}

	srcv.Env = make([]serviceEnv, 0, len(p.Env))
	for k, v := range p.Env {
		srcv.Env = append(srcv.Env, serviceEnv{Name: k, Value: v})
	}
	srcv.Env = append(srcv.Env,
		serviceEnv{Name: "__PIPE_SERVICE_NAME", Value: p.Name},
		serviceEnv{Name: "__PIPE_LOG_DIR", Value: logPath},
		serviceEnv{Name: "__PIPE_NOTIFY_HTTP", Value: fmt.Sprintf("http://localhost:%d", eventPort)},
		serviceEnv{Name: "__PIPE_MACHINE_IP", Value: machineIP},
	)

	return srcv
}

type WindowsProcessConfig struct {
	Processes []WindowsProcess `json:"processes"`
}

type supervisorState int32

const (
	stateEnabled supervisorState = iota
	stateDisabled
)

type windowsJobSupervisor struct {
	cmdRunner             boshsys.CmdRunner
	dirProvider           boshdirs.Provider
	fs                    boshsys.FileSystem
	logger                boshlog.Logger
	logTag                string
	machineIP             string
	msgCh                 chan *windowsServiceEvent
	monitor               *monitor.Monitor
	jobFailuresServerPort int
	cancelServer          chan bool

	// state *state.State
	state supervisorState
	mgr   *winsvc.Mgr
}

func (w *windowsJobSupervisor) stateSet(s supervisorState) {
	atomic.StoreInt32((*int32)(&w.state), int32(s))
}

func (w *windowsJobSupervisor) stateIs(s supervisorState) bool {
	return atomic.LoadInt32((*int32)(&w.state)) == int32(s)
}

func matchService(description string) bool {
	return description == serviceDescription
}

func NewWindowsJobSupervisor(
	cmdRunner boshsys.CmdRunner,
	dirProvider boshdirs.Provider,
	fs boshsys.FileSystem,
	logger boshlog.Logger,
	jobFailuresServerPort int,
	cancelChan chan bool,
	machineIP string,
) JobSupervisor {
	s := &windowsJobSupervisor{
		cmdRunner:   cmdRunner,
		dirProvider: dirProvider,
		fs:          fs,
		logger:      logger,
		logTag:      "windowsJobSupervisor",
		machineIP:   machineIP,
		msgCh:       make(chan *windowsServiceEvent, 8),
		jobFailuresServerPort: jobFailuresServerPort,
		cancelServer:          cancelChan,
	}
	m, err := monitor.New(-1)
	if err != nil {
		s.logger.Error(s.logTag, "Initializing monitor.Monitor: %s", err)
	}
	s.monitor = m
	s.stateSet(stateEnabled)

	mgr, err := winsvc.Connect(matchService)
	if err != nil {
		s.logger.Error(s.logTag, "Initializing winsvc.Mgr: %s", err)
	}
	s.mgr = mgr
	return s
}

func (w *windowsJobSupervisor) Reload() error {
	return nil
}

func (w *windowsJobSupervisor) Start() error {
	// Set the starttype of the service running the Agent to 'manual'.
	// This will prevent the agent from automatically starting if the
	// machine is rebooted.
	//
	// Do this here, as we know the agent has successfully connected
	// with the director and is healthy.
	w.mgr.DisableAgentAutoStart()

	if err := w.mgr.Start(); err != nil {
		return bosherr.WrapError(err, "Starting windows job process")
	}

	if err := w.fs.RemoveAll(w.stoppedFilePath()); err != nil {
		return bosherr.WrapError(err, "Removing stopped file")
	}

	w.stateSet(stateEnabled)
	return nil
}

func (w *windowsJobSupervisor) Stop() error {
	if err := w.mgr.Stop(); err != nil {
		return bosherr.WrapError(err, "Stopping services")
	}
	if err := w.fs.WriteFileString(w.stoppedFilePath(), ""); err != nil {
		return bosherr.WrapError(err, "Removing stop services")
	}
	return nil
}

func (w *windowsJobSupervisor) StopAndWait() error {
	// Stop already does this for us
	return w.Stop()
}

func (w *windowsJobSupervisor) Unmonitor() error {
	w.stateSet(stateDisabled)
	return w.mgr.Unmonitor()
}

func (w *windowsJobSupervisor) Status() (status string) {
	if w.fs.FileExists(w.stoppedFilePath()) {
		return "stopped"
	}

	sts, err := w.mgr.Status()
	if err != nil {
		fmt.Println("STATUS - ERROR:", err)
		return "failing"
	}
	if len(sts) == 0 {
		return "running"
	}
	for _, status := range sts {
		if status.State != svc.Running {
			return "failing"
		}
	}
	return "running"
}

var windowsSvcStateMap = map[svc.State]string{
	svc.Stopped:         "stopped",
	svc.StartPending:    "starting",
	svc.StopPending:     "stop_pending",
	svc.Running:         "running",
	svc.ContinuePending: "continue_pending",
	svc.PausePending:    "pause_pending",
	svc.Paused:          "paused",
}

func SvcStateString(s svc.State) string {
	return windowsSvcStateMap[s]
}

func (w *windowsJobSupervisor) Processes() ([]Process, error) {
	// NB (CEV): The process PID retrieved here is the PID of
	// the service wrapper process (WinSW) not the PID of the
	// underlying process.
	//
	// If we ever decide to populate the CPU or Memory fields
	// of the returned Processes we must find and include the
	// service's child processes - as they are what we are
	// actually interested in, and unless the application is
	// logging very heavily are what will be responsible for
	// the majority of system usage.

	sts, err := w.mgr.Status()
	if err != nil {
		return nil, bosherr.WrapError(err, "Getting windows job process status")
	}
	procs := make([]Process, len(sts))
	for i, st := range sts {
		procs[i] = Process{Name: st.Name, State: SvcStateString(st.State)}
	}
	return procs, nil
}

func (w *windowsJobSupervisor) AddJob(jobName string, jobIndex int, configPath string) error {
	configFileContents, err := w.fs.ReadFile(configPath)
	if err != nil {
		return err
	}

	if len(configFileContents) == 0 {
		w.logger.Debug(w.logTag, "Skipping job configuration for %q, empty monit config file %q", jobName, configPath)
		return nil
	}

	var processConfig WindowsProcessConfig
	err = json.Unmarshal(configFileContents, &processConfig)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	for _, process := range processConfig.Processes {
		logPath := path.Join(w.dirProvider.LogsDir(), jobName, process.Name)
		err := w.fs.MkdirAll(logPath, os.FileMode(0750))
		if err != nil {
			return bosherr.WrapErrorf(err, "Creating log directory for service '%s'", process.Name)
		}

		buf.Reset()
		serviceConfig := process.ServiceWrapperConfig(logPath, w.jobFailuresServerPort, w.machineIP)
		if err := xml.NewEncoder(&buf).Encode(serviceConfig); err != nil {
			return bosherr.WrapErrorf(err, "Rendering service config template for service '%s'", process.Name)
		}

		w.logger.Debug(w.logTag, "Configuring service wrapper for job %q with configPath %q", jobName, configPath)

		jobDir := filepath.Dir(configPath)

		processDir := filepath.Join(jobDir, process.Name)
		err = w.fs.MkdirAll(processDir, os.FileMode(0750))
		if err != nil {
			return bosherr.WrapErrorf(err, "Creating job directory for service '%s' at '%s'", process.Name, processDir)
		}

		serviceWrapperConfigFile := filepath.Join(processDir, serviceWrapperConfigFileName)
		err = w.fs.WriteFile(serviceWrapperConfigFile, buf.Bytes())
		if err != nil {
			return bosherr.WrapErrorf(err, "Saving service config file for service '%s'", process.Name)
		}

		err = w.fs.WriteFileString(filepath.Join(processDir, serviceWrapperAppConfigFileName), serviceWrapperAppConfigBody)
		if err != nil {
			return bosherr.WrapErrorf(err, "Saving app runtime config file for service '%s'", process.Name)
		}

		serviceWrapperExePath := filepath.Join(w.dirProvider.BoshBinDir(), serviceWrapperExeFileName)
		err = w.fs.CopyFile(serviceWrapperExePath, filepath.Join(processDir, serviceWrapperExeFileName))
		if err != nil {
			return bosherr.WrapErrorf(err, "Copying service wrapper in job directory '%s'", processDir)
		}

		cmdToRun := filepath.Join(processDir, serviceWrapperExeFileName)

		_, _, _, err = w.cmdRunner.RunCommand(cmdToRun, "install")
		if err != nil {
			return bosherr.WrapErrorf(err, "Creating service '%s'", process.Name)
		}
	}

	return nil
}

func (w *windowsJobSupervisor) RemoveAllJobs() error {
	return w.mgr.Delete()
}

type windowsServiceEvent struct {
	Event       string `json:"event"`
	ProcessName string `json:"processName"`
	ExitCode    int    `json:"exitCode"`
}

func (w *windowsJobSupervisor) MonitorJobFailures(handler JobFailureHandler) error {
	hl := http.HandlerFunc(func(writer http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if w.stateIs(stateDisabled) {
			return
		}
		var event windowsServiceEvent
		err := json.NewDecoder(r.Body).Decode(&event)
		if err != nil {
			w.logger.Error(w.logTag, "MonitorJobFailures received unknown request: %s", err)
			return
		}
		handler(boshalert.MonitAlert{
			Action:      "Start",
			Date:        time.Now().Format(time.RFC1123Z),
			Event:       event.Event,
			ID:          event.ProcessName,
			Service:     event.ProcessName,
			Description: fmt.Sprintf("exited with code %d", event.ExitCode),
		})
	})
	server := http_server.New(fmt.Sprintf("localhost:%d", w.jobFailuresServerPort), hl)
	process := ifrit.Invoke(server)
	for {
		select {
		case <-w.cancelServer:
			process.Signal(os.Kill)
		case err := <-process.Wait():
			if err != nil {
				return bosherr.WrapError(err, "Listen for HTTP")
			}
			return nil
		}
	}
}

func (w *windowsJobSupervisor) stoppedFilePath() string {
	return filepath.Join(w.dirProvider.MonitDir(), "stopped")
}

func (w *windowsJobSupervisor) HealthRecorder(status string) {
}
