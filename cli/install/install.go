package install

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/apex/log"
)

// FlagInstall is a struct that contains all install flags.
type FlagInstall struct {
	InstallReinstall bool
	InstallForce     bool
	InstallVerbose   bool
	InstallNoclean   bool
}

// Packet is a struct containing sys and install name of the packet.
type Packet struct {
	sysName     string
	installName string
}

// isDeprecated checks wethere version of programm is below 1.10.0.
func isDeprecated(version string) bool {
	splitedVersion := strings.Split(version, ".")
	if len(splitedVersion) < 2 {
		return false
	}
	if splitedVersion[0] == "1" && len(splitedVersion[1]) < 2 {
		return true
	}
	return false
}

func isCentOs() bool {
	cmd := exec.Command("bash", "-c", "lsb_release -a")
	readPipe, writePipe, _ := os.Pipe()
	cmd.Stdout = writePipe
	//cmd.Stderr = os.Stderr
	cmd.Start()
	cmd.Wait()
	writePipe.Close()
	var buf bytes.Buffer
	io.Copy(&buf, readPipe)
	packets := buf.String()
	return strings.Contains(packets, "CentOS")
}

// copy copies file from source to destination.
func copy(src string, dst string) error {
	// Read all content of src to data.
	data, err := ioutil.ReadFile(src)
	if err != nil {
		return err
	}
	// Write data to dst.
	err = ioutil.WriteFile(dst, data, 0777)
	return err
}

// checkDependecie checks if programm is installed.
func checkDependecie(programm string) bool {
	cmd := exec.Command(programm, "--version")
	cmd.Start()
	err := cmd.Wait()
	return err == nil
}

// checkPythonDependencie checks if python packet is installed.
func checkPythonDependencie(packet string) bool {
	cmd := exec.Command("pip3", "list")
	readPipe, writePipe, _ := os.Pipe()
	cmd.Stdout = writePipe
	cmd.Stderr = os.Stderr
	cmd.Start()
	cmd.Wait()
	writePipe.Close()
	var buf bytes.Buffer
	io.Copy(&buf, readPipe)
	packets := buf.String()
	return strings.Contains(packets, packet)
}

func CheckLibDependencie(library string) bool {
	cmd := exec.Command("bash", "-c", "ls"+" /lib/*/")
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("brew", "list")
	}
	if isCentOs() {
		cmd = exec.Command("yum", "list", "--installed")
	}
	readPipe, writePipe, _ := os.Pipe()
	cmd.Stdout = writePipe
	cmd.Stderr = os.Stderr
	cmd.Start()
	cmd.Wait()
	writePipe.Close()
	var buf bytes.Buffer
	io.Copy(&buf, readPipe)
	packets := buf.String()
	return strings.Contains(packets, library)
}

// checkTtDependencies checks if tt dependencies is installed.
func checkTtDependencies() bool {
	programms := []string{"mage", "git", "python3"}
	var miss_count = 0
	missing_pack := ""
	for i := 0; i < len(programms); i++ {
		if !checkDependecie(programms[i]) {
			miss_count++
			missing_pack += " " + programms[i]
		}
	}
	if miss_count != 0 {
		log.Error("The operation requires some dependencies.")
		log.Error("Missing packages: " + missing_pack)
		if runtime.GOOS == "darwin" {
			log.Error("You can install them by running command:\n brew install" + missing_pack)
		} else if runtime.GOOS == "linux" {
			log.Error("You can install them by running command:\n sudo apt install" + missing_pack)
		}
		log.Error("Usage: tt install -f if You already have those packages installed")
		return false
	}
	return true
}

// checkTarantoolDependencies checks if tarantool dependencies is installed.
func checkTarantoolDependencies() bool {
	var miss_count = 0
	missing_pack := ""
	missing_pack_python := ""
	programms := []Packet{{"cmake", "cmake"}, {"git", "git"}, {"python3", "python3"},
		{"make", "make"}}
	pyPackets := []Packet{{"PyYAML", "python3-yaml"}, {"six", "python3-six"},
		{"gevent", "python3-gevent"}}
	if runtime.GOOS == "darwin" {
		pyPackets = []Packet{{"PyYAML", "yaml"}, {"six", "six"},
			{"gevent", "gevent"}}
	}
	var libraries []Packet
	if isCentOs() {
		libraries = []Packet{{"libz", "zlib-devel"}, {"ncurses", "ncurses-devel"},
			{"readline", "readline-devel"}, {"openssl", "openssl-devel"},
			{"libunwind", "libunwind-devel"}, {"libicu", "libicu-devel"}}
	} else if runtime.GOOS == "darwin" {
		libraries = []Packet{{"zlib", "zlib"}, {"libiconv", "libiconv"},
			{"readline", "readline"}, {"openssl", "openssl"},
			{"curl", "curl"}, {"icu4c", "icu4c"}}
	} else if runtime.GOOS == "linux" {
		libraries = []Packet{{"libz", "zlib1g-dev"}, {"libncurses", "libncurses5-dev"},
			{"libreadline", "libreadline-dev"}, {"libssl", "libssl-dev"},
			{"libunwind", "libunwind-dev"}, {"libicu", "libicu-dev"}}
	}
	for i := 0; i < len(programms); i++ {
		if !checkDependecie(programms[i].sysName) {
			miss_count++
			missing_pack += " " + programms[i].installName
		}
	}
	for i := 0; i < len(pyPackets); i++ {
		if !checkPythonDependencie(pyPackets[i].sysName) {
			miss_count++
			missing_pack_python += " " + pyPackets[i].installName
		}
	}
	for i := 0; i < len(libraries); i++ {
		if !CheckLibDependencie(libraries[i].sysName) {
			miss_count++
			missing_pack += " " + libraries[i].installName
		}
	}
	if miss_count != 0 {
		log.Error("The operation requires some dependencies.")
		log.Error("Missing packages: " + missing_pack + " " + missing_pack_python)
		if runtime.GOOS == "darwin" || runtime.GOOS == "freebsd" {
			log.Error("You can install them by running commands:")
			log.Error("brew install" + missing_pack)
			log.Error("pip3 install" + missing_pack_python)
		} else if isCentOs() {
			log.Error("You can install them by running command:\n sudo yum install" + missing_pack)
		} else if runtime.GOOS == "linux" {
			log.Error("You can install them by running command:\n sudo apt install" + missing_pack)
		}
		log.Error("Usage: tt install -f if You already have those packages installed")
		return false
	}
	return true
}

// checkExisting checks if programm is already installed.
func checkExisting(version string, dst string) bool {
	if _, err := os.Stat(dst + "/" + version); os.IsNotExist(err) {
		return false
	} else {
		return true
	}
}

// executeCommand executes programm with given args in verbose or quiet mode.
func executeCommand(programm string, isVerbose bool, logFile *os.File, args ...string) error {
	cmd := exec.Command(programm, args...)
	if isVerbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	err := cmd.Start()
	if err != nil {
		return err
	}
	err = cmd.Wait()
	return err
}

// changeSymlink changes symlink.
func changeSymlink(version string, dst string, programm string) error {
	cmdRm := exec.Command("rm", dst+"/"+programm)
	cmdRm.Start()
	cmdRm.Wait()
	cmdLink := exec.Command("ln", "-s", version, dst+"/"+programm)
	err := cmdLink.Start()
	cmdLink.Wait()
	return err
}

// installTt installs selected version of tt.
func installTt(version string, dst string, flags *FlagInstall) error {
	if dst == "" {
		path, _ := os.Getwd()
		dst += path + "/bin"
	}
	logFile, _ := ioutil.TempFile("", "tarantool_install")

	log.Warnf("Checking existing...")
	if checkExisting(version, dst) && !flags.InstallReinstall {
		log.Warnf("That version already exists, changing symlinc...")
		err := changeSymlink(version, dst, "tt")
		log.Warnf("Done")
		return err
	}

	log.Warnf("Checking dependecies...")
	if !checkTtDependencies() && !flags.InstallForce {
		return nil
	}

	log.Warnf("Downloading tt...")
	path, err := os.MkdirTemp("", "tt_install")
	os.Chmod(path, 0777)
	if err != nil {
		return err
	}
	if !flags.InstallNoclean {
		defer os.RemoveAll(path)
	}
	err = executeCommand("git", flags.InstallVerbose, logFile, "clone",
		"https://github.com/tarantool/tt", "--recursive", path)
	if err != nil {
		logs, _ := os.ReadFile(logFile.Name())
		os.Stdout.Write(logs)
		return err
	}

	log.Warnf("Building tt...")
	err = executeCommand("bash", flags.InstallVerbose, logFile,
		"-c", "cd "+path+" && mage build")
	if err != nil {
		logs, _ := os.ReadFile(logFile.Name())
		os.Stdout.Write(logs)
		return err
	}

	log.Warnf("Copying executable...")
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		err = os.Mkdir(dst, 0777)
		if err != nil {
			return fmt.Errorf("Unable to create bindir: %s", err)
		}
	}
	if flags.InstallReinstall {
		os.Remove(dst + "/" + version)
	}
	err = copy(path+"/tt", dst+"/"+version)
	if err != nil {
		return err
	}

	err = changeSymlink(version, dst, "tt")
	log.Warnf("Done.")
	if flags.InstallNoclean {
		log.Warnf("Artifacts can be found at: " + path)
	}
	return err
}

// installTarantool installs selected version of tarantool.
func installTarantool(version string, dst string, flags *FlagInstall) error {
	tarVersion := version[10:]
	if isDeprecated(tarVersion) {
		return fmt.Errorf("Deprecated version of tarantool")
	}
	if dst == "" {
		path, _ := os.Getwd()
		dst += path + "/bin"
	}
	logFile, _ := ioutil.TempFile("", "tarantool_install")

	log.Warnf("Checking existing...")
	if checkExisting(version, dst) && !flags.InstallReinstall {
		log.Warnf("That version already exists, changing symlinc...")
		err := changeSymlink(version, dst, "tarantool")
		log.Warnf("Done")
		return err
	}

	log.Warnf("Checking dependecies...")
	if !checkTarantoolDependencies() && !flags.InstallForce {
		return nil
	}

	log.Warnf("Downloading tarantool...")
	path, err := os.MkdirTemp("", "tarantool_install")
	os.Chmod(path, 0777)
	if err != nil {
		return err
	}
	if !flags.InstallNoclean {
		defer os.RemoveAll(path)
	}
	err = executeCommand("git", flags.InstallVerbose, logFile, "clone", "-b", tarVersion,
		"--depth", "1", "https://github.com/tarantool/tarantool.git", "--recursive", path)
	if err != nil {
		logs, _ := os.ReadFile(logFile.Name())
		os.Stdout.Write(logs)
		return err
	}

	log.Warnf("Building tarantool...")
	err = executeCommand("bash", flags.InstallVerbose, logFile, "-c",
		"cd "+path+" && git submodule update --init --recursive")
	if err != nil {
		logs, _ := os.ReadFile(logFile.Name())
		os.Stdout.Write(logs)
		return err
	}
	err = executeCommand("bash", flags.InstallVerbose, logFile, "-c",
		"cd "+path+" && mkdir build")
	if err != nil {
		logs, _ := os.ReadFile(logFile.Name())
		os.Stdout.Write(logs)
		return err
	}
	err = executeCommand("bash", flags.InstallVerbose, logFile, "-c",
		"cd "+path+"/build"+" && cmake .. -DCMAKE_BUILD_TYPE=RelWithDebInfo")
	if err != nil {
		logs, _ := os.ReadFile(logFile.Name())
		os.Stdout.Write(logs)
		return err
	}
	err = executeCommand("bash", flags.InstallVerbose, logFile, "-c",
		"cd "+path+"/build"+" && make")
	if err != nil {
		logs, _ := os.ReadFile(logFile.Name())
		os.Stdout.Write(logs)
		return err
	}

	log.Warnf("Copying executable...")
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		err = os.Mkdir(dst, 0777)
		if err != nil {
			return fmt.Errorf("Unable to create bindir: %s", err)
		}
	}
	if flags.InstallReinstall {
		os.Remove(dst + "/" + version)
	}
	err = copy(path+"/build/src/tarantool", dst+"/"+version)
	if err != nil {
		return err
	}

	err = changeSymlink(version, dst, "tarantool")
	log.Warnf("Done.")
	if flags.InstallNoclean {
		log.Warnf("Artifacts can be found at: " + path)
	}
	return err
}

// Install installs programm.
func Install(args []string, binDir string, flags *FlagInstall) error {
	var err error
	if len(args) != 1 {
		return fmt.Errorf("Invallid parametrs")
	}
	if strings.Contains(args[0], "tt") {
		log.Warnf("Installing tt...")
		err = installTt(args[0], binDir, flags)
	} else if strings.Contains(args[0], "tarantool") {
		log.Warnf("Installing tarantool...")
		err = installTarantool(args[0], binDir, flags)
	} else {
		log.Warnf("Unknow application: " + args[0])
	}
	return err
}
