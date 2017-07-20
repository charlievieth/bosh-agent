package jobsupervisor_test

import (
	"errors"
	"fmt"
	"math/rand"
	"net"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"testing"
)

func TestJobsupervisor(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Job Supervisor Suite")
}

func FindOpenPort() (int, error) {
	const Base = 5000
	rand.Seed(time.Now().UnixNano())

	for i := 0; i < 50; i++ {
		port := Base + rand.Intn(10000)
		addr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("localhost:%d", port))
		if err != nil {
			return 0, err
		}
		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			continue
		}
		l.Close()
		return port, nil
	}
	return 0, errors.New("could not find open port to listen on")
}
