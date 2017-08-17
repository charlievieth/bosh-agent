package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"time"

	boshplatform "github.com/cloudfoundry/bosh-agent/platform"
	boshsettings "github.com/cloudfoundry/bosh-agent/settings"
	boshdir "github.com/cloudfoundry/bosh-agent/settings/directories"
	bosherr "github.com/cloudfoundry/bosh-utils/errors"
	boshlog "github.com/cloudfoundry/bosh-utils/logger"
	boshsys "github.com/cloudfoundry/bosh-utils/system"
)

type Bootstrap interface {
	Run() error
}

type bootstrap struct {
	fs              boshsys.FileSystem
	platform        boshplatform.Platform
	dirProvider     boshdir.Provider
	settingsService boshsettings.Service
	logger          boshlog.Logger
}

func NewBootstrap(
	platform boshplatform.Platform,
	dirProvider boshdir.Provider,
	settingsService boshsettings.Service,
	logger boshlog.Logger,
) Bootstrap {
	return bootstrap{
		fs:              platform.GetFs(),
		platform:        platform,
		dirProvider:     dirProvider,
		settingsService: settingsService,
		logger:          logger,
	}
}

const bootTag = "bootstrap"

func (boot bootstrap) Run() (err error) {
	var t time.Time
	start := time.Now()
	defer boot.logger.Debug(bootTag, "Run: %s", time.Since(start))

	t = time.Now()
	if err = boot.platform.SetupRuntimeConfiguration(); err != nil {
		return bosherr.WrapError(err, "Setting up runtime configuration")
	}
	boot.logger.Info(bootTag, "SetupRuntimeConfiguration", time.Since(t))

	t = time.Now()
	iaasPublicKey, err := boot.settingsService.PublicSSHKeyForUsername(boshsettings.VCAPUsername)
	if err != nil {
		return bosherr.WrapError(err, "Setting up ssh: Getting iaas public key")
	}
	boot.logger.Info(bootTag, "PublicSSHKeyForUsername", time.Since(t))

	t = time.Now()
	if len(iaasPublicKey) > 0 {
		if err = boot.platform.SetupSSH([]string{iaasPublicKey}, boshsettings.VCAPUsername); err != nil {
			return bosherr.WrapError(err, "Setting up iaas ssh")
		}
	}
	boot.logger.Info(bootTag, "SetupSSH (IaaS Public Key)", time.Since(t))

	t = time.Now()
	if err = boot.settingsService.LoadSettings(); err != nil {
		return bosherr.WrapError(err, "Fetching settings")
	}
	boot.logger.Info(bootTag, "LoadSettings", time.Since(t))

	t = time.Now()
	settings := boot.settingsService.GetSettings()
	boot.logger.Info(bootTag, "GetSettings", time.Since(t))

	t = time.Now()
	envPublicKeys := settings.Env.GetAuthorizedKeys()
	boot.logger.Info(bootTag, "GetAuthorizedKeys", time.Since(t))

	t = time.Now()
	if len(envPublicKeys) > 0 {
		publicKeys := envPublicKeys

		if len(iaasPublicKey) > 0 {
			publicKeys = append(publicKeys, iaasPublicKey)
		}

		if err = boot.platform.SetupSSH(publicKeys, boshsettings.VCAPUsername); err != nil {
			return bosherr.WrapError(err, "Adding env-configured ssh keys")
		}
	}
	boot.logger.Info(bootTag, "SetupSSH (public keys: %d): %s", len(envPublicKeys), time.Since(t))

	t = time.Now()
	if err = boot.setUserPasswords(settings.Env); err != nil {
		return bosherr.WrapError(err, "Settings user password")
	}
	boot.logger.Info(bootTag, "setUserPasswords: %s", time.Since(t))

	t = time.Now()
	if err = boot.platform.SetupIPv6(settings.Env.Bosh.IPv6); err != nil {
		return bosherr.WrapError(err, "Setting up IPv6")
	}
	boot.logger.Info(bootTag, "SetupIPv6: %s", time.Since(t))

	t = time.Now()
	if err = boot.platform.SetupHostname(settings.AgentID); err != nil {
		return bosherr.WrapError(err, "Setting up hostname")
	}
	boot.logger.Info(bootTag, "SetupHostname: %s", time.Since(t))

	t = time.Now()
	if err = boot.platform.SetupNetworking(settings.Networks); err != nil {
		return bosherr.WrapError(err, "Setting up networking")
	}
	boot.logger.Info(bootTag, "SetupNetworking: %s", time.Since(t))

	t = time.Now()
	if err = boot.platform.SetTimeWithNtpServers(settings.Ntp); err != nil {
		return bosherr.WrapError(err, "Setting up NTP servers")
	}
	boot.logger.Info(bootTag, "SetTimeWithNtpServers: %s", time.Since(t))

	t = time.Now()
	if err = boot.platform.SetupRawEphemeralDisks(settings.RawEphemeralDiskSettings()); err != nil {
		return bosherr.WrapError(err, "Setting up raw ephemeral disk")
	}
	boot.logger.Info(bootTag, "SetupRawEphemeralDisks: %s", time.Since(t))

	t = time.Now()
	ephemeralDiskPath := boot.platform.GetEphemeralDiskPath(settings.EphemeralDiskSettings())
	desiredSwapSizeInBytes := settings.Env.GetSwapSizeInBytes()
	boot.logger.Info(bootTag, "GetEphemeralDiskPath: %s", time.Since(t))

	t = time.Now()
	if err = boot.platform.SetupEphemeralDiskWithPath(ephemeralDiskPath, desiredSwapSizeInBytes); err != nil {
		return bosherr.WrapError(err, "Setting up ephemeral disk")
	}
	boot.logger.Info(bootTag, "SetupEphemeralDiskWithPath: %s", time.Since(t))

	t = time.Now()
	if err = boot.platform.SetupRootDisk(ephemeralDiskPath); err != nil {
		return bosherr.WrapError(err, "Setting up root disk")
	}
	boot.logger.Info(bootTag, "SetupRootDisk: %s", time.Since(t))

	t = time.Now()
	if err = boot.platform.SetupLogDir(); err != nil {
		return bosherr.WrapError(err, "Setting up log dir")
	}
	boot.logger.Info(bootTag, "SetupLogDir: %s", time.Since(t))

	t = time.Now()
	if err = boot.platform.SetupLoggingAndAuditing(); err != nil {
		return bosherr.WrapError(err, "Starting up logging and auditing utilities")
	}
	boot.logger.Info(bootTag, "SetupLoggingAndAuditing: %s", time.Since(t))

	t = time.Now()
	if err = boot.platform.SetupDataDir(); err != nil {
		return bosherr.WrapError(err, "Setting up data dir")
	}
	boot.logger.Info(bootTag, "SetupDataDir: %s", time.Since(t))

	t = time.Now()
	if err = boot.platform.SetupTmpDir(); err != nil {
		return bosherr.WrapError(err, "Setting up tmp dir")
	}
	boot.logger.Info(bootTag, "SetupTmpDir: %s", time.Since(t))

	t = time.Now()
	if err = boot.platform.SetupHomeDir(); err != nil {
		return bosherr.WrapError(err, "Setting up home dir")
	}
	boot.logger.Info(bootTag, "SetupHomeDir: %s", time.Since(t))

	t = time.Now()
	if err = boot.platform.SetupBlobsDir(); err != nil {
		return bosherr.WrapError(err, "Setting up blobs dir")
	}
	boot.logger.Info(bootTag, "SetupBlobsDir: %s", time.Since(t))

	t = time.Now()
	if err = boot.comparePersistentDisk(); err != nil {
		return bosherr.WrapError(err, "Comparing persistent disks")
	}
	boot.logger.Info(bootTag, "comparePersistentDisk: %s", time.Since(t))

	t = time.Now()
	for diskID := range settings.Disks.Persistent {
		var lastDiskID string
		diskSettings, _ := settings.PersistentDiskSettings(diskID)

		isPartitioned, err := boot.platform.IsPersistentDiskMountable(diskSettings)
		if err != nil {
			return bosherr.WrapError(err, "Checking if persistent disk is partitioned")
		}

		lastDiskID, err = boot.lastMountedCid()
		if err != nil {
			return bosherr.WrapError(err, "Fetching last mounted disk CID")
		}
		if isPartitioned && diskID == lastDiskID {
			if err = boot.platform.MountPersistentDisk(diskSettings, boot.dirProvider.StoreDir()); err != nil {
				return bosherr.WrapError(err, "Mounting persistent disk")
			}
		}
	}
	boot.logger.Info(bootTag, "PersistentDisks (%d): %s", len(settings.Disks.Persistent), time.Since(t))

	t = time.Now()
	if err = boot.platform.SetupMonitUser(); err != nil {
		return bosherr.WrapError(err, "Setting up monit user")
	}
	boot.logger.Debug(bootTag, "SetupMonitUser: %s", time.Since(t))

	t = time.Now()
	if err = boot.platform.StartMonit(); err != nil {
		return bosherr.WrapError(err, "Starting monit")
	}
	boot.logger.Debug(bootTag, "StartMonit: %s", time.Since(t))

	t = time.Now()
	if settings.Env.GetRemoveDevTools() {
		packageFileListPath := path.Join(boot.dirProvider.EtcDir(), "dev_tools_file_list")

		if !boot.fs.FileExists(packageFileListPath) {
			return nil
		}

		if err = boot.platform.RemoveDevTools(packageFileListPath); err != nil {
			return bosherr.WrapError(err, "Removing Development Tools Packages")
		}
	}
	boot.logger.Debug(bootTag, "GetRemoveDevTools: %s", time.Since(t))

	t = time.Now()
	if settings.Env.GetRemoveStaticLibraries() {
		staticLibrariesListPath := path.Join(boot.dirProvider.EtcDir(), "static_libraries_list")

		if !boot.fs.FileExists(staticLibrariesListPath) {
			return nil
		}

		if err = boot.platform.RemoveStaticLibraries(staticLibrariesListPath); err != nil {
			return bosherr.WrapError(err, "Removing static libraries")
		}
	}
	boot.logger.Debug(bootTag, "GetRemoveStaticLibraries: %s", time.Since(t))

	return nil
}

func (boot bootstrap) comparePersistentDisk() error {
	start := time.Now()
	defer boot.logger.Debug(bootTag, "comparePersistentDisk: %s", time.Since(start))

	settings := boot.settingsService.GetSettings()
	updateSettingsPath := filepath.Join(boot.platform.GetDirProvider().BoshDir(), "update_settings.json")

	if err := boot.checkLastMountedCid(settings); err != nil {
		return err
	}

	var updateSettings boshsettings.UpdateSettings

	if boot.platform.GetFs().FileExists(updateSettingsPath) {
		contents, err := boot.platform.GetFs().ReadFile(updateSettingsPath)
		if err != nil {
			return bosherr.WrapError(err, "Reading update_settings.json")
		}

		if err = json.Unmarshal(contents, &updateSettings); err != nil {
			return bosherr.WrapError(err, "Unmarshalling update_settings.json")
		}
	}

	for _, diskAssociation := range updateSettings.DiskAssociations {
		if _, ok := settings.PersistentDiskSettings(diskAssociation.DiskCID); !ok {
			return fmt.Errorf("Disk %s is not attached", diskAssociation.DiskCID)
		}
	}

	if len(settings.Disks.Persistent) > 1 {
		if len(settings.Disks.Persistent) > len(updateSettings.DiskAssociations) {
			return errors.New("Unexpected disk attached")
		}
	}

	return nil
}

func (boot bootstrap) setUserPasswords(env boshsettings.Env) error {
	start := time.Now()
	defer boot.logger.Debug(bootTag, "setUserPasswords: %s", time.Since(start))

	password := env.GetPassword()

	if !env.GetKeepRootPassword() {
		err := boot.platform.SetUserPassword(boshsettings.RootUsername, password)
		if err != nil {
			return bosherr.WrapError(err, "Setting root password")
		}
	}

	err := boot.platform.SetUserPassword(boshsettings.VCAPUsername, password)
	if err != nil {
		return bosherr.WrapError(err, "Setting vcap password")
	}

	return nil
}

func (boot bootstrap) checkLastMountedCid(settings boshsettings.Settings) error {
	start := time.Now()
	defer boot.logger.Debug(bootTag, "checkLastMountedCid: %s", time.Since(start))

	lastMountedCid, err := boot.lastMountedCid()
	if err != nil {
		return bosherr.WrapError(err, "Fetching last mounted disk CID")
	}

	if len(settings.Disks.Persistent) == 0 || lastMountedCid == "" {
		return nil
	}

	if _, ok := settings.PersistentDiskSettings(lastMountedCid); !ok {
		return fmt.Errorf("Attached disk disagrees with previous mount")
	}

	return nil
}

func (boot bootstrap) lastMountedCid() (string, error) {
	start := time.Now()
	defer boot.logger.Debug(bootTag, "lastMountedCid: %s", time.Since(start))

	managedDiskSettingsPath := filepath.Join(boot.platform.GetDirProvider().BoshDir(), "managed_disk_settings.json")
	var lastMountedCid string

	if boot.platform.GetFs().FileExists(managedDiskSettingsPath) {
		contents, err := boot.platform.GetFs().ReadFile(managedDiskSettingsPath)
		if err != nil {
			return "", bosherr.WrapError(err, "Reading managed_disk_settings.json")
		}
		lastMountedCid = string(contents)

		return lastMountedCid, nil
	}

	return "", nil
}
