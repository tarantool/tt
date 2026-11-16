package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/binary"
)

// The os-release files of the distributions, as they ship them, trimmed.
const (
	centos7Release = `NAME="CentOS Linux"
VERSION="7 (Core)"
ID="centos"
ID_LIKE="rhel fedora"
VERSION_ID="7"
PRETTY_NAME="CentOS Linux 7 (Core)"
`
	centosStream9Release = `NAME="CentOS Stream"
VERSION="9"
ID="centos"
ID_LIKE="rhel fedora"
PRETTY_NAME="CentOS Stream 9"
`
	rhel9Release = `NAME="Red Hat Enterprise Linux"
ID="rhel"
ID_LIKE="fedora"
PRETTY_NAME="Red Hat Enterprise Linux 9.4 (Plow)"
`
	rocky9Release = `NAME="Rocky Linux"
ID="rocky"
ID_LIKE="rhel centos fedora"
PRETTY_NAME="Rocky Linux 9.4 (Blue Onyx)"
`
	alma9Release = `NAME="AlmaLinux"
ID="almalinux"
ID_LIKE="rhel centos fedora"
PRETTY_NAME="AlmaLinux 9.4 (Seafoam Ocelot)"
`
	fedoraRelease = `NAME="Fedora Linux"
ID=fedora
PRETTY_NAME="Fedora Linux 40 (Workstation Edition)"
`
	oracleRelease = `NAME="Oracle Linux Server"
ID="ol"
ID_LIKE="fedora"
`
	ubuntuRelease = `PRETTY_NAME="Ubuntu 24.04.1 LTS"
NAME="Ubuntu"
ID=ubuntu
ID_LIKE=debian
`
	debianRelease = `PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"
NAME="Debian GNU/Linux"
ID=debian
`
	mintRelease = `NAME="Linux Mint"
ID=linuxmint
ID_LIKE="ubuntu debian"
`
	archRelease = `NAME="Arch Linux"
PRETTY_NAME="Arch Linux"
ID=arch
`
	alpineRelease = `NAME="Alpine Linux"
ID=alpine
`
)

func TestOSReleaseFamily(t *testing.T) {
	for name, test := range map[string]struct {
		release string
		want    osFamily
	}{
		"CentOS 7":        {centos7Release, osFamilyRedHat},
		"CentOS Stream 9": {centosStream9Release, osFamilyRedHat},
		"RHEL 9":          {rhel9Release, osFamilyRedHat},
		"Rocky Linux 9":   {rocky9Release, osFamilyRedHat},
		"AlmaLinux 9":     {alma9Release, osFamilyRedHat},
		"Fedora 40":       {fedoraRelease, osFamilyRedHat},
		"Oracle Linux":    {oracleRelease, osFamilyRedHat},
		"Ubuntu 24.04":    {ubuntuRelease, osFamilyDebian},
		"Debian 12":       {debianRelease, osFamilyDebian},
		"Linux Mint":      {mintRelease, osFamilyDebian},
		"upper case":      {"ID=CentOS\n", osFamilyRedHat},
		"single quotes":   {"ID='rocky'\n", osFamilyRedHat},
		"Arch Linux":      {archRelease, osFamilyUnknown},
		"Alpine Linux":    {alpineRelease, osFamilyUnknown},
		"empty":           {"", osFamilyUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "os-release")
			require.NoError(t, os.WriteFile(path, []byte(test.release), 0o644))

			release, err := readOSRelease(path)
			require.NoError(t, err)
			assert.Equal(t, test.want, release.family())
		})
	}

	_, err := readOSRelease(filepath.Join(t.TempDir(), "missing"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestDependenciesMessage(t *testing.T) {
	for _, test := range []struct {
		name    string
		family  osFamily
		manager string
		want    string
	}{
		{
			name: "CentOS 7", family: osFamilyRedHat,
			want: "Missing packages: cmake git make gcc gcc-c++ libstdc++-static perl\n" +
				"You can install them by running command:\n" +
				" sudo yum install cmake git make gcc gcc-c++ libstdc++-static perl\n",
		},
		{
			name: "dnf", family: osFamilyRedHat, manager: "dnf",
			want: " sudo dnf install cmake git make gcc gcc-c++ libstdc++-static perl\n",
		},
		{
			name: "Debian", family: osFamilyDebian,
			want: "Missing packages: cmake git make build-essential coreutils sed\n" +
				"You can install them by running command:\n" +
				" sudo apt install cmake git make build-essential coreutils sed\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Neither the programs nor the package managers are there.
			commands := t.TempDir()
			if test.manager != "" {
				require.NoError(t, os.WriteFile(filepath.Join(commands, test.manager),
					[]byte("#!/bin/sh\nexit 1\n"), 0o755))
			}

			t.Setenv("PATH", commands)

			err := dependenciesInstalledOn(binary.ProgramCe, test.family)

			var missing missingPackagesError

			require.ErrorAs(t, err, &missing)
			assert.Contains(t, err.Error(), test.want)
			assert.NotContains(t, err.Error(), "Unknown OS")
		})
	}
}
