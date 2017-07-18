// +build windows

package jobsupervisor_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	boshalert "github.com/cloudfoundry/bosh-agent/agent/alert"
	boshdirs "github.com/cloudfoundry/bosh-agent/settings/directories"
	boshlog "github.com/cloudfoundry/bosh-utils/logger"
	boshsys "github.com/cloudfoundry/bosh-utils/system"

	. "github.com/cloudfoundry/bosh-agent/jobsupervisor"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
)

const jobFailuresServerPort = 5000

const DefaultMachineIP = "127.0.0.1"

const (
	DefaultTimeout   = time.Second * 15
	DefaultInterval  = time.Millisecond * 500
	DefaultEventPort = 2825
)

var (
	StartStopExe string
	TempDir      string
)

var _ = AfterSuite(func() {
	os.RemoveAll(TempDir)
	gexec.CleanupBuildArtifacts()
})

var _ = BeforeSuite(func() {
	var err error
	TempDir, err = ioutil.TempDir("", "bosh-")
	Expect(err).ToNot(HaveOccurred())

	StartStopExe, err = gexec.Build("testdata/StartStop/main.go")
	Expect(err).ToNot(HaveOccurred())
})

func testWindowsConfigs(jobName string) (WindowsProcessConfig, bool) {
	// create temp file - used by stop-start jobs
	f, err := ioutil.TempFile(TempDir, "stopfile-")
	Expect(err).ToNot(HaveOccurred())
	tmpFileName := f.Name()
	f.Close()

	m := map[string]WindowsProcessConfig{
		"say-hello": WindowsProcessConfig{
			Processes: []WindowsProcess{
				{
					Name:       fmt.Sprintf("say-hello-1-%d", time.Now().UnixNano()),
					Executable: "powershell",
					Args:       []string{"/C", "Write-Host \"Hello 1\"; Start-Sleep 10"},
				},
				{
					Name:       fmt.Sprintf("say-hello-2-%d", time.Now().UnixNano()),
					Executable: "powershell",
					Args:       []string{"/C", "Write-Host \"Hello 2\"; Start-Sleep 10"},
				},
			},
		},
		"say-hello-syslog": WindowsProcessConfig{
			Processes: []WindowsProcess{
				{
					Name:       fmt.Sprintf("say-hello-1-%d", time.Now().UnixNano()),
					Executable: "powershell",
					Args:       []string{"/C", "Write-Host \"Hello\"; Start-Sleep 10"},
					Env: map[string]string{
						"__PIPE_SYSLOG_HOST":      "localhost",
						"__PIPE_SYSLOG_PORT":      "10202",
						"__PIPE_SYSLOG_TRANSPORT": "udp",
					},
				},
			},
		},
		"flapping": WindowsProcessConfig{
			Processes: []WindowsProcess{
				{
					Name:       fmt.Sprintf("flapping-1-%d", time.Now().UnixNano()),
					Executable: "powershell",
					Args:       []string{"/C", "Write-Host \"Flapping-1\"; Start-Sleep 1; exit 2"},
				},
				{
					Name:       fmt.Sprintf("flapping-2-%d", time.Now().UnixNano()),
					Executable: "powershell",
					Args:       []string{"/C", "Write-Host \"Flapping-2\"; Start-Sleep 1; exit 2"},
				},
				{
					Name:       fmt.Sprintf("flapping-3-%d", time.Now().UnixNano()),
					Executable: "powershell",
					Args:       []string{"/C", "Write-Host \"Flapping-3\"; Start-Sleep 1; exit 2"},
				},
			},
		},
		"looping": WindowsProcessConfig{
			Processes: []WindowsProcess{
				{
					Name:       fmt.Sprintf("looping-1-%d", time.Now().UnixNano()),
					Executable: "powershell",
					Args:       []string{"/C", "While($true) { Write-Host \"Looping\"; Start-Sleep 1; }"},
				},
				{
					Name:       fmt.Sprintf("looping-2-%d", time.Now().UnixNano()),
					Executable: "powershell",
					Args:       []string{"/C", "While($true) { Start-Process -NoNewWindow powershell.exe -ArgumentList 'Start-Sleep','50'; Start-Sleep 1 }"},
				},
			},
		},
		"stop-executable": WindowsProcessConfig{
			Processes: []WindowsProcess{
				{
					Name:       fmt.Sprintf("stop-executable-1-%d", time.Now().UnixNano()),
					Executable: StartStopExe,
					Args:       []string{"start", tmpFileName},
					Stop: &StopCommand{
						Executable: StartStopExe,
						Args:       []string{"stop", tmpFileName},
					},
				},
			},
		},
	}
	conf, ok := m[jobName]
	return conf, ok
}

func concurrentStopConfig() WindowsProcessConfig {
	// Five jobs that print in a loop
	const JobCount = 5

	// Two wait jobs that use stop scripts to until the
	// other jobs have stopped before stopping themselves
	const WaitCount = 2

	// Job config

	const jobTemplate = `
	While ($true) {
		Write-Host "Hello %d"
		Start-Sleep -Milliseconds 200
	}`

	var conf WindowsProcessConfig
	for i := 0; i < JobCount; i++ {
		p := WindowsProcess{
			Name:       fmt.Sprintf("job-%d-%d", i, time.Now().UnixNano()),
			Executable: "powershell",
			Args:       []string{"-Command", fmt.Sprintf(jobTemplate, i)},
		}
		conf.Processes = append(conf.Processes, p)
	}

	// Wait config

	const waitTemplate = `
	$n=0
	While (Test-Path %[1]s) {
		if ($n++ -gt 60) {
			Exit 1
		}
		Write-Host "File exists: %[1]s"
		Start-Sleep -Seconds 1
	}`

	const stopTemplate = `
	function CountRunning {
		return (get-wmiobject win32_service -filter "description='vcap'" | Where { $_.State -eq "Running" }).Count
	}
	$Limit=60
	while (CountRunning -gt %[1]d) {
		if ($Limit-- -eq 0) {
			Remove-Item -Path %[2]s -Force
			Exit 1 # Error
		}
		Start-Sleep -Seconds 1
	}
	Remove-Item -Path %[2]s -Force;`

	createStopFile := func() string {
		f, err := ioutil.TempFile(TempDir, "stopfile-")
		Expect(err).ToNot(HaveOccurred())
		path := f.Name()
		f.Close()
		return path
	}

	writeTemplate := func(name, template string, args ...interface{}) string {
		// Create a temp directory so that we may specify the file
		// name and extension - TempFile doesn't let you do this.
		dirname, err := ioutil.TempDir(TempDir, "")
		Expect(err).ToNot(HaveOccurred())
		path := filepath.Join(dirname, name)

		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		Expect(err).ToNot(HaveOccurred())
		defer f.Close()

		// Write template to file
		_, err = fmt.Fprintf(f, template, args...)
		Expect(err).ToNot(HaveOccurred())

		return path
	}

	for i := 0; i < WaitCount; i++ {
		stopFile := createStopFile()
		waitpath := writeTemplate("wait.ps1", waitTemplate, stopFile)
		stoppath := writeTemplate("stop.ps1", stopTemplate, WaitCount, stopFile)

		wait := WindowsProcess{
			Name:       fmt.Sprintf("wait-%d-%d", i, time.Now().UnixNano()),
			Executable: "powershell",
			Args:       []string{"-File", waitpath},
			Stop: &StopCommand{
				Executable: "powershell",
				Args:       []string{"-File", stoppath},
			},
		}
		conf.Processes = append(conf.Processes, wait)
	}

	return conf
}

func buildPipeExe() error {
	pathToPipeCLI, err := gexec.Build("github.com/cloudfoundry/bosh-agent/jobsupervisor/pipe")
	if err != nil {
		return err
	}
	SetPipeExePath(pathToPipeCLI)
	return nil
}

var _ = Describe("WindowsJobSupervisor", func() {
	Context("add jobs and control services", func() {
		BeforeEach(func() {
			if runtime.GOOS != "windows" {
				Skip("Pending on non-Windows")
			}
		})

		var (
			once              sync.Once
			fs                boshsys.FileSystem
			logger            boshlog.Logger
			basePath          string
			logDir            string
			exePath           string
			jobDir            string
			processConfigPath string
			jobSupervisor     JobSupervisor
			runner            boshsys.CmdRunner
			logOut            *bytes.Buffer
			logErr            *bytes.Buffer
		)

		BeforeEach(func() {
			once.Do(func() { Expect(buildPipeExe()).To(Succeed()) })

			const testExtPath = "testdata/job-service-wrapper"

			logOut = bytes.NewBufferString("")
			logErr = bytes.NewBufferString("")

			logger = boshlog.NewWriterLogger(boshlog.LevelDebug, logOut, logErr)
			fs = boshsys.NewOsFileSystem(logger)

			var err error
			basePath, err = ioutil.TempDir(TempDir, "")
			Expect(err).ToNot(HaveOccurred())
			fs.MkdirAll(basePath, 0755)

			binPath := filepath.Join(basePath, "bosh", "bin")
			fs.MkdirAll(binPath, 0755)

			logDir = path.Join(basePath, "sys", "log")
			fs.MkdirAll(binPath, 0755)

			exePath = filepath.Join(binPath, "job-service-wrapper.exe")

			err = fs.CopyFile(testExtPath, exePath)
			Expect(err).ToNot(HaveOccurred())

			logDir = path.Join(basePath, "sys", "log")
		})

		WriteJobConfig := func(configContents WindowsProcessConfig) (string, error) {
			dirProvider := boshdirs.NewProvider(basePath)
			runner = boshsys.NewExecCmdRunner(logger)
			jobSupervisor = NewWindowsJobSupervisor(runner, dirProvider, fs, logger, jobFailuresServerPort,
				make(chan bool), DefaultMachineIP)
			if err := jobSupervisor.RemoveAllJobs(); err != nil {
				return "", err
			}
			processConfigContents, err := json.Marshal(configContents)
			if err != nil {
				return "", err
			}

			jobDir, err = fs.TempDir("testWindowsJobSupervisor")
			processConfigPath = filepath.Join(jobDir, "monit")

			err = fs.WriteFile(processConfigPath, processConfigContents)
			return processConfigPath, err
		}

		AddJob := func(jobName string) (WindowsProcessConfig, error) {
			conf, ok := testWindowsConfigs(jobName)
			if !ok {
				return conf, fmt.Errorf("Invalid Windows Config Process name: %s", jobName)
			}
			confPath, err := WriteJobConfig(conf)
			if err != nil {
				return conf, err
			}
			return conf, jobSupervisor.AddJob(jobName, 0, confPath)
		}

		// AddFlappingJob adds and starts flapping job jobName and waits for it
		// to fail at least once.
		AddFlappingJob := func(jobName string) (*WindowsProcessConfig, error) {
			conf, err := AddJob(jobName)
			if err != nil {
				return nil, err
			}
			if err := jobSupervisor.Start(); err != nil {
				return nil, err
			}
			m, err := mgr.Connect()
			if err != nil {
				return nil, err
			}
			defer m.Disconnect()

			var svcs []*mgr.Service
			defer func() {
				for _, s := range svcs {
					s.Close()
				}
			}()

			for _, proc := range conf.Processes {
				s, err := m.OpenService(proc.Name)
				if err != nil {
					return nil, err
				}
				svcs = append(svcs, s)
			}

			const Interval = time.Millisecond * 100
			Eventually(func() (string, error) {
				var state string
				for _, s := range svcs {
					st, err := s.Query()
					if err != nil {
						return "", err
					}
					state = SvcStateString(st.State)
					if st.State == svc.Stopped {
						break
					}
				}
				return state, nil
			}, DefaultTimeout, Interval).Should(Equal(SvcStateString(svc.Stopped)))

			return &conf, nil
		}

		GetServiceState := func(serviceName string) (svc.State, error) {
			m, err := mgr.Connect()
			if err != nil {
				return 0, err
			}
			defer m.Disconnect()
			s, err := m.OpenService(serviceName)
			if err != nil {
				return 0, err
			}
			defer s.Close()
			st, err := s.Query()
			if err != nil {
				return 0, err
			}
			return st.State, nil
		}

		GetServiceInfo := func(svcName string) (svc.Status, mgr.Config, error) {
			m, err := mgr.Connect()
			if err != nil {
				return svc.Status{}, mgr.Config{}, err
			}
			defer m.Disconnect()

			s, err := m.OpenService(svcName)
			if err != nil {
				return svc.Status{}, mgr.Config{}, err
			}
			defer s.Close()

			status, err := s.Query()
			if err != nil {
				return svc.Status{}, mgr.Config{}, err
			}

			conf, err := s.Config()
			if err != nil {
				return svc.Status{}, mgr.Config{}, err
			}

			return status, conf, nil
		}

		AfterEach(func() {
			Expect(jobSupervisor.Stop()).To(Succeed())
			Expect(jobSupervisor.RemoveAllJobs()).To(Succeed())
			Expect(fs.RemoveAll(jobDir)).To(Succeed())
			Expect(fs.RemoveAll(logDir)).To(Succeed())
		})

		Describe("Processes", func() {
			It("list the process under vcap description", func() {

				conf, err := AddJob("say-hello")
				names := make(map[string]bool)
				for _, p := range conf.Processes {
					names[p.Name] = true
				}

				Expect(err).ToNot(HaveOccurred())
				Expect(jobSupervisor.Start()).To(Succeed())

				allProcsAreRunning := func() bool {
					procs, err := jobSupervisor.Processes()
					Expect(err).ToNot(HaveOccurred())
					Expect(len(procs)).To(Equal(len(conf.Processes)))
					for _, p := range procs {
						Expect(names).To(HaveKey(p.Name))
						if p.State != "running" && p.State != "starting" {
							return false
						}
					}
					return true
				}
				Eventually(allProcsAreRunning, DefaultTimeout, DefaultInterval).Should(BeTrue())
			})

			It("lists the status of stopped process under vcap description", func() {

				conf, err := AddJob("say-hello")
				Expect(err).ToNot(HaveOccurred())
				Expect(jobSupervisor.Start()).To(Succeed())
				Expect(jobSupervisor.Stop()).To(Succeed())

				procs, err := jobSupervisor.Processes()
				Expect(err).ToNot(HaveOccurred())
				Expect(len(procs)).To(Equal(len(conf.Processes)))

				names := make(map[string]bool)
				for _, p := range conf.Processes {
					names[p.Name] = true
				}
				for _, p := range procs {
					Expect(names).To(HaveKey(p.Name))
					Expect(p.State).To(Equal("stopped"))
					Expect(int(p.CPU.Total)).To(Equal(0))
					Expect(int(p.CPU.Total)).To(Equal(0))
					Expect(int(p.Memory.Kb)).To(Equal(0))
				}
			})
		})

		Describe("AddJob", func() {
			It("creates a service with vcap description", func() {
				conf, err := AddJob("say-hello")
				Expect(err).ToNot(HaveOccurred())

				for _, proc := range conf.Processes {
					status, conf, err := GetServiceInfo(proc.Name)
					Expect(err).ToNot(HaveOccurred())

					Expect(conf.Description).To(Equal("vcap"))
					Expect(status.State).To(Equal(svc.Stopped))
				}
			})

			Context("when monit file is empty", func() {
				BeforeEach(func() {
					Expect(fs.WriteFileString(processConfigPath, "")).To(Succeed())
				})

				It("does not return an error", func() {
					_, err := AddJob("say-hello")
					Expect(err).ToNot(HaveOccurred())
				})
			})
		})

		Describe("Start", func() {
			var conf WindowsProcessConfig
			BeforeEach(func() {
				var err error
				conf, err = AddJob("say-hello")
				Expect(err).ToNot(HaveOccurred())
			})

			It("will start all the services", func() {
				Expect(jobSupervisor.Start()).To(Succeed())

				for _, proc := range conf.Processes {
					state, err := GetServiceState(proc.Name)
					Expect(err).ToNot(HaveOccurred())
					Expect(state).To(Equal(svc.Running))
				}
			})

			It("writes logs to job log directory", func() {
				Expect(jobSupervisor.Start()).To(Succeed())

				for i, proc := range conf.Processes {
					readLogFile := func() (string, error) {
						return fs.ReadFileString(path.Join(logDir, "say-hello", proc.Name, "job-service-wrapper.out.log"))
					}
					Eventually(readLogFile, DefaultTimeout, DefaultInterval).Should(ContainSubstring(fmt.Sprintf("Hello %d", i+1)))
				}
			})

			It("sets the LOG_DIR env variable for the pipe", func() {
				Expect(jobSupervisor.Start()).To(Succeed())

				validFile := func(name string) func() error {
					return func() error {
						fi, err := os.Stat(name)
						if err != nil {
							return err
						}
						if fi.Size() == 0 {
							return fmt.Errorf("empty file: %s", name)
						}
						return nil
					}
				}

				for _, proc := range conf.Processes {
					pipeLogPath := filepath.Join(logDir, "say-hello", proc.Name, "pipe.log")
					Eventually(validFile(pipeLogPath), DefaultTimeout, DefaultInterval).Should(Succeed())
				}
			})

			It("sets the SERVICE_NAME env variable for the pipe", func() {
				Expect(jobSupervisor.Start()).To(Succeed())

				fileContains := func(filename, substring string) func() error {
					return func() error {
						b, err := ioutil.ReadFile(filename)
						if err != nil {
							return err
						}
						if !bytes.Contains(b, []byte(substring)) {
							return fmt.Errorf("file %s does not contain substring: %s", filename, substring)
						}
						return nil
					}
				}

				for _, proc := range conf.Processes {
					pipeLogPath := filepath.Join(logDir, "say-hello", proc.Name, "pipe.log")
					Eventually(fileContains(pipeLogPath, proc.Name), DefaultTimeout, DefaultInterval).Should(Succeed())
				}
			})
		})

		Describe("Status", func() {
			Context("with jobs", func() {
				BeforeEach(func() {
					_, err := AddJob("say-hello")
					Expect(err).ToNot(HaveOccurred())
				})

				Context("when running", func() {
					It("reports that the job is 'Running'", func() {
						Expect(jobSupervisor.Start()).To(Succeed())

						Expect(jobSupervisor.Status()).To(Equal("running"))
					})
				})

				Context("when stopped", func() {
					It("reports that the job is 'Stopped'", func() {
						Expect(jobSupervisor.Start()).To(Succeed())

						Expect(jobSupervisor.Stop()).To(Succeed())

						Expect(jobSupervisor.Status()).To(Equal("stopped"))
					})
				})
			})

			Context("with no jobs", func() {
				Context("when running", func() {
					It("reports that the job is 'Running'", func() {
						Expect(jobSupervisor.Start()).To(Succeed())

						Expect(jobSupervisor.Status()).To(Equal("running"))
					})
				})
			})
		})

		Describe("Unmonitor", func() {
			var conf WindowsProcessConfig
			BeforeEach(func() {
				var err error
				conf, err = AddJob("say-hello")
				Expect(err).ToNot(HaveOccurred())
			})

			It("sets service status to Disabled", func() {
				Expect(jobSupervisor.Unmonitor()).To(Succeed())

				for _, proc := range conf.Processes {
					_, conf, err := GetServiceInfo(proc.Name)
					Expect(err).ToNot(HaveOccurred())
					Expect(uint(conf.StartType)).To(Equal(uint(mgr.StartDisabled)))
				}
			})
		})

		Describe("StopAndWait", func() {
			It("waits for the services to be stopped", func() {
				conf, err := AddJob("looping")
				Expect(err).ToNot(HaveOccurred())

				Expect(jobSupervisor.Start()).To(Succeed())
				Expect(jobSupervisor.StopAndWait()).To(Succeed())

				for _, proc := range conf.Processes {
					st, err := GetServiceState(proc.Name)
					Expect(err).To(Succeed())
					Expect(SvcStateString(st)).To(Equal(SvcStateString(svc.Stopped)))
				}
			})

			It("stops flapping service", func() {
				conf, err := AddFlappingJob("flapping")
				Expect(err).To(Succeed())
				Expect(jobSupervisor.StopAndWait()).To(Succeed())

				Consistently(func() bool {
					stopped := true
					for _, proc := range conf.Processes {
						st, err := GetServiceState(proc.Name)
						if err != nil || st != svc.Stopped {
							stopped = false
						}
					}
					return stopped
				}, time.Second*6, time.Millisecond*10).Should(BeTrue())
			})
		})

		Describe("Stop", func() {

			It("sets service status to Stopped", func() {
				conf, err := AddJob("say-hello")
				Expect(err).ToNot(HaveOccurred())

				Expect(jobSupervisor.Start()).To(Succeed())
				Expect(jobSupervisor.Stop()).To(Succeed())

				for _, proc := range conf.Processes {
					Eventually(func() (string, error) {
						st, err := GetServiceState(proc.Name)
						return SvcStateString(st), err
					}).Should(Equal(SvcStateString(svc.Stopped)))
				}
			})

			It("can start a stopped service", func() {
				conf, err := AddJob("say-hello")
				Expect(err).ToNot(HaveOccurred())

				Expect(jobSupervisor.Start()).To(Succeed())
				Expect(jobSupervisor.Stop()).To(Succeed())

				for _, proc := range conf.Processes {
					Eventually(func() (string, error) {
						st, err := GetServiceState(proc.Name)
						return SvcStateString(st), err
					}).Should(Equal(SvcStateString(svc.Stopped)))
				}

				Expect(jobSupervisor.Start()).To(Succeed())
				for _, proc := range conf.Processes {
					Eventually(func() (string, error) {
						st, err := GetServiceState(proc.Name)
						return SvcStateString(st), err
					}).Should(Equal(SvcStateString(svc.Running)))
				}
			})

			It("stops flapping services", func() {
				conf, err := AddFlappingJob("flapping")
				Expect(err).ToNot(HaveOccurred())

				Expect(jobSupervisor.Stop()).To(Succeed())

				proc := conf.Processes[0]
				Eventually(func() (string, error) {
					st, err := GetServiceState(proc.Name)
					return SvcStateString(st), err
				}, time.Second*6).Should(Equal(SvcStateString(svc.Stopped)))

				Consistently(func() (string, error) {
					st, err := GetServiceState(proc.Name)
					return SvcStateString(st), err
				}, time.Second*6, time.Millisecond*100).Should(Equal(SvcStateString(svc.Stopped)))
			})

			It("stops flapping services and gives a status of stopped", func() {
				const wait = time.Second * 6
				const freq = time.Millisecond * 100
				const loops = int(time.Second * 10 / freq)

				_, err := AddFlappingJob("flapping")
				Expect(err).ToNot(HaveOccurred())

				Expect(jobSupervisor.Stop()).To(Succeed())
				for i := 0; i < loops && jobSupervisor.Status() != "stopped"; i++ {
					time.Sleep(freq)
				}
				Consistently(jobSupervisor.Status, wait).Should(Equal("stopped"))
			})

			It("stops services concurrently", func() {
				conf := concurrentStopConfig()
				confPath, err := WriteJobConfig(conf)
				Expect(err).To(Succeed())
				Expect(jobSupervisor.AddJob("ConcurrentWait", 0, confPath)).To(Succeed())

				Expect(jobSupervisor.Start()).To(Succeed())
				Expect(jobSupervisor.Stop()).To(Succeed())
			})
		})

		Describe("StopCommand", func() {
			It("uses the stop executable to stop the process", func() {
				conf, err := AddJob("stop-executable")
				Expect(err).ToNot(HaveOccurred())

				Expect(jobSupervisor.Start()).To(Succeed())
				Expect(jobSupervisor.Stop()).To(Succeed())

				for _, proc := range conf.Processes {
					Eventually(func() (string, error) {
						st, err := GetServiceState(proc.Name)
						return SvcStateString(st), err
					}, DefaultTimeout, DefaultInterval).Should(Equal(SvcStateString(svc.Stopped)))
				}
			})
		})

		Describe("MonitorJobFailures", func() {
			var cancelServer chan bool
			var dirProvider boshdirs.Provider
			const failureRequest = `{
				"event": "pid failed",
				"exitCode": 55,
				"processName": "nats"
			}`
			BeforeEach(func() {
				dirProvider = boshdirs.NewProvider(basePath)
				runner = boshsys.NewExecCmdRunner(logger)
				cancelServer = make(chan bool)
				jobSupervisor = NewWindowsJobSupervisor(runner, dirProvider, fs, logger, jobFailuresServerPort,
					cancelServer, DefaultMachineIP)
			})

			AfterEach(func() {
				cancelServer <- true
			})

			doJobFailureRequest := func(payload string, port int) error {
				url := fmt.Sprintf("http://localhost:%d", port)
				r := bytes.NewReader([]byte(payload))
				_, err := http.Post(url, "application/json", r)
				return err
			}

			expectedMonitAlert := func(recieved boshalert.MonitAlert) interface{} {
				date, err := time.Parse(time.RFC1123Z, recieved.Date)
				if err != nil {
					return err
				}
				return boshalert.MonitAlert{
					ID:          "nats",
					Service:     "nats",
					Event:       "pid failed",
					Action:      "Start",
					Date:        date.Format(time.RFC1123Z),
					Description: "exited with code 55",
				}
			}

			It("sends alerts for a flapping service", func() {

				var handledAlert boshalert.MonitAlert
				alertReceived := make(chan (bool), 1)
				failureHandler := func(alert boshalert.MonitAlert) (err error) {
					handledAlert = alert
					alertReceived <- true
					return
				}
				go jobSupervisor.MonitorJobFailures(failureHandler)

				_, err := AddFlappingJob("flapping")
				Expect(err).To(Succeed())
				Eventually(alertReceived, time.Second*6).Should(Receive())
				Expect(handledAlert.ID).To(ContainSubstring("flapping"))
				Expect(handledAlert.Event).To(Equal("pid failed"))
			})

			It("receives job failures from the service wrapper via HTTP", func() {
				var handledAlert boshalert.MonitAlert

				failureHandler := func(alert boshalert.MonitAlert) (err error) {
					handledAlert = alert
					return
				}

				go jobSupervisor.MonitorJobFailures(failureHandler)

				err := doJobFailureRequest(failureRequest, jobFailuresServerPort)
				Expect(err).ToNot(HaveOccurred())

				Expect(handledAlert).To(Equal(expectedMonitAlert(handledAlert)))
			})

			It("stops sending failures after a call to Unmonitor", func() {
				var handledAlert boshalert.MonitAlert
				failureHandler := func(alert boshalert.MonitAlert) (err error) {
					handledAlert = alert
					return
				}
				go jobSupervisor.MonitorJobFailures(failureHandler)

				// Unmonitor jobs
				Expect(jobSupervisor.Unmonitor()).To(Succeed())

				err := doJobFailureRequest(failureRequest, jobFailuresServerPort)
				Expect(err).ToNot(HaveOccurred())

				// Should match empty MonitAlert
				Expect(handledAlert).To(Equal(boshalert.MonitAlert{}))
			})

			It("re-monitors all jobs after a call to start", func() {
				var handledAlert boshalert.MonitAlert
				failureHandler := func(alert boshalert.MonitAlert) (err error) {
					handledAlert = alert
					return
				}
				go jobSupervisor.MonitorJobFailures(failureHandler)

				// Unmonitor jobs
				Expect(jobSupervisor.Unmonitor()).To(Succeed())

				err := doJobFailureRequest(failureRequest, jobFailuresServerPort)
				Expect(err).ToNot(HaveOccurred())

				// Should match empty MonitAlert
				Expect(handledAlert).To(Equal(boshalert.MonitAlert{}))

				// Start should re-monitor all jobs
				Expect(jobSupervisor.Start()).To(Succeed())

				err = doJobFailureRequest(failureRequest, jobFailuresServerPort)
				Expect(err).ToNot(HaveOccurred())

				Expect(handledAlert).To(Equal(expectedMonitAlert(handledAlert)))
			})

			It("ignores unknown requests", func() {
				var didHandleAlert bool

				failureHandler := func(alert boshalert.MonitAlert) (err error) {
					didHandleAlert = true
					return
				}

				go jobSupervisor.MonitorJobFailures(failureHandler)

				err := doJobFailureRequest(`some bad request`, jobFailuresServerPort)
				Expect(err).ToNot(HaveOccurred())
				Expect(didHandleAlert).To(BeFalse())
				Expect(logErr.Bytes()).To(ContainSubstring("MonitorJobFailures received unknown request"))
			})

			It("returns an error when it fails to bind", func() {
				failureHandler := func(alert boshalert.MonitAlert) (err error) { return }

				go jobSupervisor.MonitorJobFailures(failureHandler)
				time.Sleep(50 * time.Millisecond)
				err := jobSupervisor.MonitorJobFailures(failureHandler)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when the WindowsProcess has syslog environment variables", func() {
			var ServerConn *net.UDPConn
			var syslogReceived chan (string)
			BeforeEach(func() {
				ServerAddr, err := net.ResolveUDPAddr("udp", ":10202")
				Expect(err).To(Succeed())
				ServerConn, err = net.ListenUDP("udp", ServerAddr)
				Expect(err).To(Succeed())

				syslogReceived = make(chan (string), 1)
				go func() {
					buf := make([]byte, 1024)
					for {
						n, _, err := ServerConn.ReadFromUDP(buf)
						if err == nil {
							syslogReceived <- string(buf[0:n])
						} else {
							return
						}
					}
				}()
			})
			AfterEach(func() {
				ServerConn.Close()
			})

			// Test that the syslog message s matches pattern:
			// <6>2017-02-01T10:14:58-05:00 127.0.0.1 say-hello-1-123[100]: Hello 1
			matchSyslogMsg := func(s string) {
				const tmpl = "<6>%s %s say-hello-1-%d[%d]: Hello"
				var (
					id        int
					pid       int
					timeStamp string
					ipAddr    string
				)
				s = strings.TrimSpace(s)
				n, err := fmt.Sscanf(s, tmpl, &timeStamp, &ipAddr, &id, &pid)
				if n != 4 || err != nil {
					Expect(fmt.Errorf("Got %q, does not match template %q (%d %s)",
						s, tmpl, n, err)).To(Succeed())
				}

				_, err = time.Parse(time.RFC3339, timeStamp)
				Expect(err).To(Succeed())

				Expect(ipAddr).To(Equal(DefaultMachineIP))

				Expect(id).ToNot(Equal(0))
				Expect(pid).ToNot(Equal(0))
			}

			It("report the logs", func(done Done) {
				_, err := AddJob("say-hello-syslog")
				Expect(err).To(Succeed())
				Expect(jobSupervisor.Start()).To(Succeed())
				syslogMsg := <-syslogReceived
				matchSyslogMsg(syslogMsg)
				close(done)
			}, 20)
		})
	})

	Describe("WindowsProcess#ServiceWrapperConfig", func() {
		It("adds the pipe.exe environment variables to the winsw XML", func() {
			proc := WindowsProcess{
				Name:       "Name",
				Executable: "Executable",
				Args:       []string{"A"},
			}
			srvc := proc.ServiceWrapperConfig("LogPath", 123, DefaultMachineIP)
			envs := make(map[string]string)
			for _, e := range srvc.Env {
				envs[e.Name] = e.Value
			}
			Expect(envs["__PIPE_LOG_DIR"]).To(Equal("LogPath"))
			Expect(envs["__PIPE_NOTIFY_HTTP"]).To(Equal(fmt.Sprintf("http://localhost:%d", 123)))
		})

		Context("when the WindowsProcess has environment variables", func() {
			It("adds them to the marshalled WindowsServiceWrapperConfig XML", func() {
				proc := WindowsProcess{
					Name:       "Name",
					Executable: "Executable",
					Args:       []string{"A", "B"},
					Env: map[string]string{
						"Key_1": "Val_1",
						"Key_2": "Val_2",
					},
				}
				srvc := proc.ServiceWrapperConfig("LogPath", 0, DefaultMachineIP)
				srvcHash := map[string]string{}
				for _, e := range srvc.Env {
					srvcHash[e.Name] = e.Value
				}

				for key, value := range proc.Env {
					Expect(value).To(Equal(srvcHash[key]))
				}
			})
		})

		Context("when stop arguments or executable are provided", func() {
			var proc WindowsProcess
			BeforeEach(func() {
				proc = WindowsProcess{
					Name:       "Name",
					Executable: "Executable",
					Args:       []string{"Start_1", "Start_2"},
				}
			})

			It("uses 'startargument' instead of 'argment'", func() {
				proc.Stop = &StopCommand{
					Executable: "STOPPER",
					Args:       []string{"Stop_1", "Stop_2"},
				}
				srvc := proc.ServiceWrapperConfig("LogPath", 0, DefaultMachineIP)
				Expect(srvc.Arguments).To(HaveLen(0))
				args := append([]string{proc.Executable}, proc.Args...)
				Expect(srvc.StartArguments).To(Equal(args))
				Expect(srvc.StopArguments).To(Equal(proc.Stop.Args))
				Expect(srvc.StopExecutable).To(Equal(proc.Stop.Executable))
			})

			// FIXME (CEV & MH): This is temporary workaround until this is fixed
			// in WinSW.
			It("it only adds stop executable if stop args are supplied", func() {
				proc.Stop = &StopCommand{
					Executable: "STOPPER",
				}
				srvc := proc.ServiceWrapperConfig("LogPath", 0, DefaultMachineIP)
				args := append([]string{proc.Executable}, proc.Args...)
				Expect(srvc.Arguments).To(Equal(args))
				Expect(srvc.StartArguments).To(HaveLen(0))
				Expect(srvc.StopArguments).To(HaveLen(0))
			})

			It("uses the process executable when no stop executable is provided - not pipe.exe", func() {
				proc.Stop = &StopCommand{
					Args: []string{"Stop_1"},
				}
				srvc := proc.ServiceWrapperConfig("LogPath", 0, DefaultMachineIP)
				Expect(srvc.StopExecutable).To(Equal(proc.Executable))
			})
		})
	})
})
