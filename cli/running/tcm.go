package running

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/tarantool/tt/sdk/scope"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cluster"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/running/internal/layout"
	"github.com/tarantool/tt/v3/cli/util"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/instances"
	"github.com/tarantool/tt/v3/internal/supervisor"
)

// TCM, the Tarantool Cluster Manager, runs as an instance of its
// application: the record with kind: tcm (instances.KindTCM). It runs as an
// external process does (IsExternal), under a watchdog, in its data
// directory <root>/var/lib/<app>/<inst>/, with its output in tt.log, and
// tt does three things more for it at every start (tcmSpec):
//
//   - it finds its binary: the TCM the lock of the application bundles, as
//     tt package install put it under <root>/versions/tcm/, else the active
//     <root>/bin/tcm of the scope, which tt binary install tcm sets
//     (resolveTCMBinary);
//   - it writes its config, tcm.yaml in the run directory (tcmSettings): the
//     http address of the record; the config storage of the application, the
//     etcd or the Tarantool config storage config.yaml points to, under the
//     prefix /tcm/<app> of its own, or else the embedded etcd of TCM, with
//     its data in the data directory; the cluster of the application as the
//     one TCM starts with, in initial-settings.clusters; and the config of
//     the user from the config field of the record merged on top;
//   - it gives TCM the password of its admin user for the first start, as
//     TCM_SECURITY_BOOTSTRAP_PASSWORD, read by the watchdog from its file at
//     every start of TCM, so that no other file holds it: the file
//     password_file of the record names, or admin.secret in the data
//     directory, which tt generates with the mode 0600 on the first start.
//     A password the user sets in the environment or in the config of TCM
//     wins over both, and tt then gives none.
//
// TCM is up once its HTTP address answers (tcmAnswers).

const (
	// tcmConfigFileName is the config tt writes for TCM, in its run
	// directory.
	tcmConfigFileName = "tcm.yaml"
	// tcmSecretFileName is the password tt generates for the admin user of
	// TCM, in its data directory.
	tcmSecretFileName = "admin.secret"
	// tcmEtcdStateFileName keeps the addresses tt chose for the embedded etcd
	// of TCM, in its data directory: etcd keeps its peer address in its data.
	tcmEtcdStateFileName = "embedded-etcd.json"
	// tcmEtcdDirName is the data directory of the embedded etcd of TCM, in
	// the data directory of TCM.
	tcmEtcdDirName = "etcd"
	// TCMAdminUser is the user TCM creates on its first start.
	TCMAdminUser = "admin"
	// tcmDefaultHost and tcmDefaultPort are the defaults of http.host and
	// http.port of TCM.
	tcmDefaultHost = "127.0.0.1"
	tcmDefaultPort = 8080
	// tcmPasswordEnv is the variable of security.bootstrap-password, the
	// password of the first login of the admin user of TCM.
	tcmPasswordEnv = "TCM_SECURITY_BOOTSTRAP_PASSWORD" //nolint:gosec // G101: a name.
	// tcmPasswordKey is the key of that password in the config of TCM.
	tcmPasswordKey = "security.bootstrap-password"
	// tcmSecretMode is the mode of a file tt writes a secret into.
	tcmSecretMode = 0o600
	// tcmPasswordLength is the length of the password tt generates.
	tcmPasswordLength = 32
	// tcmDefaultClusterID is the id of the cluster TCM has from its first
	// start, which initial-settings.clusters customizes by this id.
	tcmDefaultClusterID = "00000000-0000-0000-0000-000000000000"
	// tcmEtcdMemberName is the name of the member of the embedded etcd.
	tcmEtcdMemberName = "tcm"
	// tcmPrefixRoot is where the data of TCM goes in the config storage of
	// its application: /tcm/<app>, apart from the cluster config.
	tcmPrefixRoot = "/tcm"
	// tcmClustersKey is initial-settings.clusters, whose items are merged
	// by name.
	tcmClustersKey = "initial-settings.clusters"
)

var (
	// errNoTCM is wrapped by the error for TCM whose binary is not found.
	errNoTCM = errors.New("no TCM to run")
	// errTCMConfig is wrapped by the error for a config of TCM of the user
	// that cannot be read.
	errTCMConfig = errors.New("cannot read the config of TCM")
	// errTCMSecret is wrapped by the error for a password file of TCM that
	// cannot be read.
	errTCMSecret = errors.New("cannot read the password of TCM")
	// errEmptySecret is the reason a password file holds no password.
	errEmptySecret = errors.New("the file is empty")
)

// TCMCtx is what the record of TCM says, with the paths of what tt writes
// for it, and how TCM is reached once it is resolved to start.
type TCMCtx struct {
	// HTTP is the http field of the record, empty for the default of TCM.
	HTTP string
	// Overlay is the config of TCM of the user, the config field of the
	// record resolved against the directory of the pair; empty for none.
	Overlay string
	// PasswordFile is password_file of the record, resolved so; empty for
	// none.
	PasswordFile string
	// PairConfig is config.yaml of the pair, whose config storage TCM
	// shares; empty when the pair has none.
	PairConfig string
	// ConfigFile is the config tt writes for TCM: tcm.yaml in its run
	// directory.
	ConfigFile string
	// SecretFile is where tt generates the password of the admin user:
	// admin.secret in its data directory.
	SecretFile string
	// Access is how TCM is reached, as its start resolved it (tcmSpec);
	// zero before.
	Access TCMAccess
}

// TCMAccess is how TCM is reached, and what it works with. It holds no
// password: only where the password is.
type TCMAccess struct {
	// URL is the address of the web interface of TCM.
	URL string `json:"url" yaml:"url"`
	// User is the user to log in as.
	User string `json:"user" yaml:"user"`
	// PasswordFile is the file the password of the first login is in, which
	// tt gives TCM; empty when the user gives it otherwise, PasswordFrom.
	PasswordFile string `json:"password_file,omitempty" yaml:"password_file,omitempty"`
	// PasswordFrom says where the password the user set is.
	PasswordFrom string `json:"password_from,omitempty" yaml:"password_from,omitempty"`
	// Storage names the storage TCM keeps its data in.
	Storage string `json:"storage" yaml:"storage"`
	// Note says what TCM cannot do for the application, such as show a
	// cluster whose config is a file; empty when nothing.
	Note string `json:"note,omitempty" yaml:"note,omitempty"`
}

// noTCMAccess is TCMAccess before TCM is resolved to start.
func noTCMAccess() TCMAccess {
	return TCMAccess{URL: "", User: "", PasswordFile: "", PasswordFrom: "", Storage: "", Note: ""}
}

// NoTCM is the TCMCtx of an instance that is not TCM.
func NoTCM() TCMCtx {
	return TCMCtx{
		HTTP: "", Overlay: "", PasswordFile: "", PairConfig: "", ConfigFile: "", SecretFile: "",
		Access: noTCMAccess(),
	}
}

// Line tells how to log into TCM, for tt instance start: its URL, the user,
// and where the password is, never the password.
func (access *TCMAccess) Line() string {
	password := "password in " + access.PasswordFile
	if access.PasswordFile == "" {
		password = "password from " + access.PasswordFrom
	}

	return fmt.Sprintf("%s (user %s, %s)", access.URL, access.User, password)
}

// IsTCM reports whether inst is TCM, the record with kind: tcm.
func IsTCM(inst *InstanceCtx) bool {
	return inst.Kind == instances.KindTCM
}

// setTCMCtx makes instCtx, built for the record inst with the paths of a
// Tarantool instance, the context of TCM: it runs in its data directory, as
// an external process without workdir does (setExternalCtx), and the paths
// of its record are resolved against the directory of pair.
func setTCMCtx(instCtx *InstanceCtx, inst instances.Instance, scopeLayout layout.ScopeLayout,
	pair *app.Pair,
) {
	instCtx.DataDir = scopeLayout.DataDir()
	instCtx.Workdir = instCtx.DataDir
	instCtx.ProcessPIDFile = scopeLayout.ProcessPidFile()
	instCtx.WalDir = ""
	instCtx.MemtxDir = ""
	instCtx.VinylDir = ""
	instCtx.ConsoleSocket = ""
	instCtx.BinaryPort = ""
	instCtx.ClusterConfigPath = ""

	record := inst.TCM
	if record == nil {
		record = &instances.TCM{HTTP: "", Config: "", PasswordFile: ""}
	}

	instCtx.TCM = TCMCtx{
		HTTP:         record.HTTP,
		Overlay:      pairPath(pair.Dir, record.Config),
		PasswordFile: pairPath(pair.Dir, record.PasswordFile),
		PairConfig:   pair.ConfigFile,
		ConfigFile:   filepath.Join(instCtx.RunDir, tcmConfigFileName),
		SecretFile:   filepath.Join(instCtx.DataDir, tcmSecretFileName),
		Access:       noTCMAccess(),
	}
}

// pairPath resolves path, a field of a record, against dir, the directory of
// its pair; empty stays empty.
func pairPath(dir, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}

	return filepath.Join(dir, path)
}

// resolveTCMBinary returns the binary of TCM inst runs (findTCMBinary),
// checked against the signatures with the integrity checks on.
func resolveTCMBinary(inst *InstanceCtx) (string, error) {
	path, err := findTCMBinary(inst)
	if err != nil {
		return "", fmt.Errorf("instance %q of %q: %w", inst.InstName, inst.AppName, err)
	}

	err = checkSigned(inst, "binary", path)
	if err != nil {
		return "", err
	}

	return path, nil
}

// findTCMBinary finds the binary of TCM: the version app.manifest.lock of the
// application bundles, installed under <root>/versions/tcm/ of its scope as
// tt package install installs the _runtime/tcm of a package, else the active
// <root>/bin/tcm of the scope, which tt binary install tcm selects. PATH is
// not looked at: TCM runs what the package or the scope chose.
func findTCMBinary(inst *InstanceCtx) (string, error) {
	program := binary.ProgramTcm
	bundled := ""

	lock, err := readAppLock(inst.AppDir)
	if err == nil && lock != nil {
		bundled = lock.BundledTcm
	}

	if bundled != "" {
		path := filepath.Join(binary.InstallationPath(inst.Root, program, bundled),
			program.Exec())
		if binary.IsRegularFile(path) {
			return exec.LookPath(path) //nolint:wrapcheck // The caller names the instance.
		}
	}

	active := filepath.Join(inst.Root, binary.BinDirName, program.Exec())

	path, err := exec.LookPath(active)
	if err == nil {
		return path, nil
	}

	return "", fmt.Errorf("%w: %s", errNoTCM, tcmInstallHint(inst, bundled, active))
}

// tcmInstallHint tells where TCM was looked for, and how to install it.
func tcmInstallHint(inst *InstanceCtx, bundled, active string) string {
	where := "there is no " + active
	if bundled != "" {
		where = fmt.Sprintf("%s bundles TCM %s, which is not under %s, and %s",
			lockFileOf(inst.AppDir), bundled, filepath.Join(inst.Root, binary.VersionsDirName),
			where)
	}

	version := "<version>"
	if bundled != "" {
		version = bundled
	}

	scopeFlag := ""
	if inst.Scope != app.ScopeProject {
		scopeFlag = " --scope " + inst.Scope.String()
	}

	wanted := ""

	man, err := readAppManifest(inst.AppDir)
	if err == nil && man != nil && man.Platform.Tcm.Version != "" {
		wanted = fmt.Sprintf(" (a version [platform].tcm allows, %q)", man.Platform.Tcm.Version)
	}

	return fmt.Sprintf("%s; install it with tt binary install tcm %s%s%s; TCM is a "+
		"product of Tarantool Enterprise, downloaded with the credentials of the customer "+
		"zone of tarantool.io (--credentials-file)", where, version, scopeFlag, wanted)
}

// lockFileOf is the lock of the application in dir.
func lockFileOf(dir string) string {
	return filepath.Join(dir, "app.manifest.lock")
}

// readAppLock reads the lock of the application in dir; nil without one.
func readAppLock(dir string) (*manifest.Lock, error) {
	data, err := os.ReadFile(lockFileOf(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil //nolint:nilnil // No lock is no error.
	}

	if err != nil {
		return nil, fmt.Errorf("cannot read the lock: %w", err)
	}

	lock, err := manifest.ParseLock(data)
	if err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", lockFileOf(dir), err)
	}

	return lock, nil
}

// readAppManifest reads the manifest of the application in dir; nil
// without one.
func readAppManifest(dir string) (*manifest.Manifest, error) {
	path := filepath.Join(dir, scope.ManifestFileName)

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil //nolint:nilnil // No manifest is no error.
	}

	if err != nil {
		return nil, fmt.Errorf("cannot read the manifest: %w", err)
	}

	man, _, err := manifest.ParseManifest(data)
	if err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", path, err)
	}

	return man, nil
}

// CheckTCMEdition refuses TCM in an application whose manifest runs it on
// Tarantool Community Edition (manifest.CheckTCM). An application without
// a manifest, a project of templates alone, says no edition: it gets a
// warning instead, since TCM manages clusters of Tarantool Enterprise.
func CheckTCMEdition(inst *InstanceCtx) (string, error) {
	man, err := readAppManifest(inst.AppDir)
	if err != nil {
		return "", fmt.Errorf("instance %q of %q: %w", inst.InstName, inst.AppName, err)
	}

	if man == nil {
		return fmt.Sprintf("%s: there is no %s in %s to say the edition of Tarantool the "+
			"application runs on; TCM manages clusters of Tarantool Enterprise alone",
			GetAppInstanceName(*inst), scope.ManifestFileName, inst.AppDir), nil
	}

	err = manifest.CheckTCM(man.Platform)
	if err != nil {
		return "", fmt.Errorf("instance %q of %q: %w", inst.InstName, inst.AppName, err)
	}

	return "", nil
}

// checkTCM checks what has to hold before TCM starts: the edition of its
// application (CheckTCMEdition), whose warning it returns, its binary, the
// config of the user, which has to be YAML, and the password file of its
// record, which has to hold a password. Every problem is reported at once.
func checkTCM(inst *InstanceCtx) (string, error) {
	warning, editionErr := CheckTCMEdition(inst)
	_, binaryErr := ResolveBinary(inst)

	var overlayErr, passwordErr error

	if inst.TCM.Overlay != "" {
		_, overlayErr = readTCMOverlay(inst)
	}

	if inst.TCM.PasswordFile != "" {
		_, err := readSecretFile(inst.TCM.PasswordFile)
		if err != nil {
			passwordErr = fmt.Errorf("instance %q of %q: %w %s (password_file): %w",
				inst.InstName, inst.AppName, errTCMSecret, inst.TCM.PasswordFile, err)
		}
	}

	return warning, errors.Join(editionErr, binaryErr, overlayErr, passwordErr)
}

// CheckTCM checks TCM inst for tt instance check, as its start checks it
// (checkTCM), and returns its binary and the warnings of the check.
func CheckTCM(inst *InstanceCtx) (string, []string, error) {
	warning, err := checkTCM(inst)
	if err != nil {
		return "", nil, err
	}

	path, err := ResolveBinary(inst)
	if err != nil {
		return "", nil, err
	}

	var warnings []string

	if warning != "" {
		warnings = append(warnings, warning)
	}

	return path, warnings, nil
}

// tcmSpec is the Spec of TCM: its binary runs with the config tt writes,
// -c=<run dir>/tcm.yaml, in its data directory, with the env of its record
// on top of the inherited environment, as an external process runs
// (externalSpec), and stops with SIGTERM. The password of the admin user is
// generated, when tt is to give it, and the variable that gives it is read
// from its file by the watchdog (tcmEnvFiles). What the start resolved goes
// to inst.TCM.Access.
func tcmSpec(inst *InstanceCtx, opts specOptions) (supervisor.Spec, error) {
	warning, err := checkTCM(inst)
	if err != nil {
		return supervisor.Spec{}, err
	}

	if warning != "" {
		opts.warnf("%s", warning)
	}

	path, err := ResolveBinary(inst)
	if err != nil {
		return supervisor.Spec{}, err
	}

	wrap := func(err error) error {
		return fmt.Errorf("instance %q of %q: %w", inst.InstName, inst.AppName, err)
	}

	for _, dir := range []string{inst.DataDir, inst.RunDir} {
		err = util.CreateDirectory(dir, defaultDirPerms)
		if err != nil {
			return supervisor.Spec{}, wrap(err)
		}
	}

	settings, access, err := tcmSettings(inst, true)
	if err != nil {
		return supervisor.Spec{}, wrap(err)
	}

	err = writeTCMConfig(inst, settings)
	if err != nil {
		return supervisor.Spec{}, wrap(err)
	}

	if access.PasswordFile == inst.TCM.SecretFile {
		err = ensureSecret(inst.TCM.SecretFile)
		if err != nil {
			return supervisor.Spec{}, wrap(err)
		}
	}

	inst.TCM.Access = access

	env := append(os.Environ(), "PWD="+inst.Workdir)

	for _, name := range slices.Sorted(maps.Keys(inst.Env)) {
		env = append(env, name+"="+inst.Env[name])
	}

	return supervisor.Spec{
		Path:                path,
		Args:                []string{"-c=" + inst.TCM.ConfigFile},
		Env:                 env,
		Dir:                 inst.Workdir,
		Stdin:               nil,
		Stdout:              opts.stdout,
		Stderr:              opts.stderr,
		StopSignal:          syscall.SIGTERM,
		ForwardStopSignal:   false,
		StopTimeout:         instanceStopTimeout,
		ProcessGroup:        true,
		ReloadSignal:        0,
		ForwardReloadSignal: false,
	}, nil
}

// tcmEnvFiles are the variables the watchdog of TCM reads from their files
// at every start of TCM (LaunchSpec.EnvFiles): the password of the admin user,
// when tt gives it.
func tcmEnvFiles(inst *InstanceCtx) map[string]string {
	if inst.TCM.Access.PasswordFile == "" {
		return nil
	}

	return map[string]string{tcmPasswordEnv: inst.TCM.Access.PasswordFile}
}

// DescribeTCM tells how TCM inst is reached and what it works with, for
// tt instance status: as its watchdog runs it, when it runs, and as its next
// start would resolve it otherwise, without writing anything.
func DescribeTCM(inst *InstanceCtx) (TCMAccess, error) {
	launch := RunningLaunch(inst)
	if launch != nil && launch.Instance.TCM.Access.URL != "" {
		return launch.Instance.TCM.Access, nil
	}

	_, access, err := tcmSettings(inst, false)
	if err != nil {
		return TCMAccess{}, fmt.Errorf("instance %q of %q: %w", inst.InstName, inst.AppName, err)
	}

	return access, nil
}

// tcmSettings returns the config of TCM inst, as tcm.yaml holds it, and how
// TCM is reached with it. With prepare, what the config needs is made ready
// for the start, the addresses of the embedded etcd chosen; without it,
// nothing is written.
func tcmSettings(inst *InstanceCtx, prepare bool) (map[string]any, TCMAccess, error) {
	access := TCMAccess{
		URL: "", User: TCMAdminUser, PasswordFile: "", PasswordFrom: "", Storage: "", Note: "",
	}

	host, port := tcmDefaultHost, strconv.Itoa(tcmDefaultPort)

	if inst.TCM.HTTP != "" {
		var err error

		host, port, err = net.SplitHostPort(inst.TCM.HTTP)
		if err != nil {
			return nil, access, fmt.Errorf("http %q: %w", inst.TCM.HTTP, err)
		}
	}

	portNumber, err := strconv.Atoi(port)
	if err != nil {
		return nil, access, fmt.Errorf("http %q: %w", inst.TCM.HTTP, err)
	}

	settings := map[string]any{
		"http": map[string]any{"host": host, "port": portNumber},
		// TCM logs to its standard output, which goes to tt.log, without
		// the colors of a terminal.
		"log": map[string]any{
			"default": map[string]any{"output": "stdout", "no-colorized": true},
		},
	}

	conn, stored, err := pairStorage(inst)
	if err != nil {
		return nil, access, err
	}

	var clusterStorage map[string]any

	if stored {
		settings["storage"] = storageSettings(&conn, tcmPrefix(inst))
		clusterStorage = clusterStorageSettings(&conn)
		access.Storage = cluster.DescribeStorage(conn.Kind, conn.Endpoints, tcmPrefix(inst), "") +
			", the config storage of the application"
	} else {
		embedded, err := embeddedEtcdOf(inst, prepare)
		if err != nil {
			return nil, access, err
		}

		settings["storage"] = embeddedSettings(inst, embedded)
		clusterStorage = embeddedClusterSettings(inst, embedded)
		access.Storage = describeEmbedded(inst, embedded)
		access.Note = fileConfigNote(inst, embedded)
	}

	settings["initial-settings"] = map[string]any{
		"clusters": []any{map[string]any{
			"id":   tcmDefaultClusterID,
			"name": inst.AppName,
			"description": fmt.Sprintf("The cluster of the application %s, declared by tt",
				inst.AppName),
			"storage-connection": clusterStorage,
		}},
	}

	if inst.TCM.Overlay != "" {
		overlay, err := readTCMOverlay(inst)
		if err != nil {
			return nil, access, err
		}

		mergeTCMSettings(settings, overlay, "")
	}

	access.URL = tcmURL(inst, settings)
	access.PasswordFile, access.PasswordFrom = tcmPasswordSource(inst, settings)

	return settings, access, nil
}

// tcmPrefix is the prefix TCM keeps its data under in the config storage of
// its application: /tcm/<app>, so that the TCMs of the applications that
// share a storage keep apart, and apart from the cluster configs.
func tcmPrefix(inst *InstanceCtx) string {
	return tcmPrefixRoot + "/" + inst.AppName
}

// pairStorage returns the config storage config.yaml of the pair of inst
// points to, read from the file alone; stored is false when the pair has no
// config.yaml, or one that is the cluster config itself.
func pairStorage(inst *InstanceCtx) (cluster.StorageConnection, bool, error) {
	var none cluster.StorageConnection

	if inst.TCM.PairConfig == "" {
		return none, false, nil
	}

	data, err := readAppFile(inst, inst.TCM.PairConfig)
	if errors.Is(err, fs.ErrNotExist) {
		return none, false, nil
	}

	if err != nil {
		return none, false, err
	}

	cfg, err := cluster.BuildGoConfigFromBytes(context.Background(), data)
	if err != nil {
		return none, false, fmt.Errorf("cannot read %s: %w", inst.TCM.PairConfig, err)
	}

	conn, stored, err := cluster.ReadStorageConnection(cfg)
	if err != nil {
		return none, false, fmt.Errorf("the config storage of %s: %w", inst.TCM.PairConfig, err)
	}

	return conn, stored, nil
}

// readAppFile reads a file of the application of inst, through its
// integrity checks when they are on.
func readAppFile(inst *InstanceCtx, path string) ([]byte, error) {
	if inst.Integrity == nil {
		return os.ReadFile(path) //nolint:wrapcheck // The error names the file.
	}

	reader, err := inst.Integrity.Repository.Read(path)
	if err != nil {
		return nil, err
	}

	defer func() { _ = reader.Close() }()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}

	return data, nil
}

// putCredentials sets the user and the password of conn into fields, with
// the names TCM gives them, when conn has them.
func putCredentials(fields map[string]any, conn *cluster.StorageConnection) {
	if conn.Username != "" {
		fields["username"] = conn.Username
	}

	if conn.Password != "" {
		fields["password"] = conn.Password
	}
}

// etcdTLS returns the tls group of a connection of TCM to the etcd of conn,
// nil when it is not over TLS.
func etcdTLS(conn *cluster.StorageConnection) map[string]any {
	ssl := conn.SSL
	if !ssl.Enable {
		return nil
	}

	group := map[string]any{"enabled": true, "insecure-skip-verify": !ssl.VerifyPeer}

	for key, value := range map[string]string{
		"cert-file": ssl.CertFile, "key-file": ssl.KeyFile, "trusted-ca-file": ssl.CaFile,
	} {
		if value != "" {
			group[key] = value
		}
	}

	return group
}

// storageSettings returns the storage group of the config of TCM that keeps
// its data in the config storage conn, under prefix.
func storageSettings(conn *cluster.StorageConnection, prefix string) map[string]any {
	if conn.Kind == cluster.StorageKindTarantool {
		fields := map[string]any{"addrs": conn.Endpoints, keyPrefix: prefix}
		putCredentials(fields, conn)

		return map[string]any{keyProvider: providerTarantool, providerTarantool: fields}
	}

	fields := map[string]any{keyEndpoints: conn.Endpoints, keyPrefix: prefix}
	putCredentials(fields, conn)

	tlsGroup := etcdTLS(conn)
	if tlsGroup != nil {
		fields["tls"] = tlsGroup
	}

	return map[string]any{keyProvider: providerEtcd, providerEtcd: fields}
}

// The keys and the values of the config of TCM tt writes more than once.
const (
	keyPrefix         = "prefix"
	keyProvider       = "provider"
	keyEndpoints      = "endpoints"
	providerEtcd      = "etcd"
	providerTarantool = "tarantool"
	keyEtcdConnection = "etcd-connection"
)

// clusterStorageSettings returns the storage-connection of the cluster of the
// application in initial-settings.clusters: the config storage conn, under
// the prefix of the cluster config.
func clusterStorageSettings(conn *cluster.StorageConnection) map[string]any {
	fields := map[string]any{keyEndpoints: conn.Endpoints, keyPrefix: conn.Prefix}
	putCredentials(fields, conn)

	if conn.Kind == cluster.StorageKindTarantool {
		return map[string]any{keyProvider: providerTarantool, "tarantool-connection": fields}
	}

	tlsGroup := etcdTLS(conn)
	if tlsGroup != nil {
		fields["tls"] = tlsGroup
	}

	return map[string]any{keyProvider: providerEtcd, keyEtcdConnection: fields}
}

// embeddedEtcd are the addresses of the embedded etcd of TCM: the one its
// clients connect to and the one of its peers.
type embeddedEtcd struct {
	Client string `json:"client"`
	Peer   string `json:"peer"`
}

// embeddedEtcdOf returns the addresses of the embedded etcd of TCM inst: those
// tt chose on its first start, kept in its data directory, since etcd keeps
// its peer address in its data. With prepare they are chosen when there are
// none yet, as two free ports of 127.0.0.1, so that the etcd of TCM does not
// take the port of an etcd of the host, or of the TCM of another
// application; without it none is chosen.
func embeddedEtcdOf(inst *InstanceCtx, prepare bool) (embeddedEtcd, error) {
	var none embeddedEtcd

	path := filepath.Join(inst.DataDir, tcmEtcdStateFileName)

	data, err := os.ReadFile(path)
	if err == nil {
		var state embeddedEtcd

		err = json.Unmarshal(data, &state)
		if err != nil || state.Client == "" || state.Peer == "" {
			return none, fmt.Errorf("cannot read the addresses of the embedded etcd of TCM "+
				"from %s: remove it for tt to choose them anew: %w", path, err)
		}

		return state, nil
	}

	if !errors.Is(err, fs.ErrNotExist) {
		return none, fmt.Errorf("cannot read the addresses of the embedded etcd of TCM: %w", err)
	}

	if !prepare {
		return none, nil
	}

	ports, err := freePorts(2) //nolint:mnd // The client and the peer.
	if err != nil {
		return none, fmt.Errorf("cannot choose the ports of the embedded etcd of TCM: %w", err)
	}

	state := embeddedEtcd{
		Client: "http://127.0.0.1:" + strconv.Itoa(ports[0]),
		Peer:   "http://127.0.0.1:" + strconv.Itoa(ports[1]),
	}

	data, err = json.Marshal(&state)
	if err != nil {
		return none, fmt.Errorf("encoding the addresses of the embedded etcd of TCM: %w", err)
	}

	err = writeFileWhole(path, append(data, '\n'), tcmSecretMode)
	if err != nil {
		return none, err
	}

	return state, nil
}

// freePorts returns count distinct TCP ports of 127.0.0.1 that are free now.
func freePorts(count int) ([]int, error) {
	listeners := make([]net.Listener, 0, count)

	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()

	ports := make([]int, 0, count)

	var config net.ListenConfig

	for range count {
		listener, err := config.Listen(context.Background(), "tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err //nolint:wrapcheck // The caller says what for.
		}

		listeners = append(listeners, listener)

		addr, _ := listener.Addr().(*net.TCPAddr)

		ports = append(ports, addr.Port)
	}

	return ports, nil
}

// embeddedDir is the data directory of the embedded etcd of TCM inst.
func embeddedDir(inst *InstanceCtx) string {
	return filepath.Join(inst.DataDir, tcmEtcdDirName)
}

// embeddedSettings returns the storage group of the config of TCM that keeps
// its data in its embedded etcd, a single member at the addresses of
// embedded, with its data in the data directory of TCM.
func embeddedSettings(inst *InstanceCtx, embedded embeddedEtcd) map[string]any {
	embed := map[string]any{"enabled": true, "workdir": embeddedDir(inst)}
	etcd := map[string]any{keyPrefix: tcmPrefix(inst), "embed": embed}

	if embedded.Client != "" {
		etcd[keyEndpoints] = []string{embedded.Client}
		embed["name"] = tcmEtcdMemberName
		embed[keyEndpoints] = []string{embedded.Client}
		embed["advertises"] = []string{embedded.Client}
		embed["peer-endpoints"] = []string{embedded.Peer}
		embed["peer-advertises"] = []string{embedded.Peer}
		embed["initial-cluster"] = tcmEtcdMemberName + "=" + embedded.Peer
		embed["initial-cluster-state"] = "new"
		embed["initial-cluster-token"] = "tcm-" + inst.AppName
	}

	return map[string]any{keyProvider: providerEtcd, providerEtcd: etcd}
}

// embeddedPrefix is the prefix of the cluster of the application in the
// embedded etcd of TCM: where a cluster config published there would be.
func embeddedPrefix(inst *InstanceCtx) string {
	return "/" + inst.AppName
}

// embeddedClusterSettings returns the storage-connection of the cluster of an
// application whose cluster config is no config storage: the embedded etcd
// of TCM, under the prefix /<app>, which holds nothing until a cluster
// config is published there (fileConfigNote).
func embeddedClusterSettings(inst *InstanceCtx, embedded embeddedEtcd) map[string]any {
	fields := map[string]any{keyPrefix: embeddedPrefix(inst)}
	if embedded.Client != "" {
		fields[keyEndpoints] = []string{embedded.Client}
	}

	return map[string]any{keyProvider: providerEtcd, keyEtcdConnection: fields}
}

// describeEmbedded names the embedded etcd of TCM for tt instance status.
func describeEmbedded(inst *InstanceCtx, embedded embeddedEtcd) string {
	at := "on ports tt chooses at its first start"
	if embedded.Client != "" {
		at = embedded.Client
	}

	return fmt.Sprintf("the embedded etcd of TCM, %s, with its data in %s", at, embeddedDir(inst))
}

// fileConfigNote says what TCM does not see of an application whose cluster
// config is not kept in a config storage: TCM reads a cluster config from a
// storage alone.
func fileConfigNote(inst *InstanceCtx, embedded embeddedEtcd) string {
	config := "has no cluster config in a config storage, which is the only place TCM " +
		"reads one from"
	if inst.TCM.PairConfig != "" {
		config = "keeps its cluster config in the file " + inst.TCM.PairConfig +
			", and TCM reads one from a config storage alone"
	}

	storage := "the embedded etcd of TCM"
	if embedded.Client != "" {
		storage += " " + embedded.Client
	}

	return fmt.Sprintf("the application %s %s: TCM shows the cluster %s from %s, prefix %q, "+
		"empty until a cluster config is published there; keep the cluster config in etcd "+
		"(config.etcd in config.yaml) and publish it with tt cluster publish %s for TCM to "+
		"see and manage the cluster", inst.AppName, config, inst.AppName, storage,
		embeddedPrefix(inst), inst.AppName)
}

// readTCMOverlay reads the config of TCM of the user, the config field of
// the record, with its dotted keys, such as storage.etcd.embed.enabled, which
// TCM takes as well, expanded into groups.
func readTCMOverlay(inst *InstanceCtx) (map[string]any, error) {
	wrap := func(err error) error {
		return fmt.Errorf("instance %q of %q: %w %s (config): %w", inst.InstName, inst.AppName,
			errTCMConfig, inst.TCM.Overlay, err)
	}

	data, err := readAppFile(inst, inst.TCM.Overlay)
	if err != nil {
		return nil, wrap(err)
	}

	var overlay map[string]any

	err = yaml.Unmarshal(data, &overlay)
	if err != nil {
		return nil, wrap(err)
	}

	return expandDotted(overlay), nil
}

// expandDotted returns settings with every key that has dots, such as
// http.port, turned into groups, http: {port: ...}, and the groups merged.
func expandDotted(settings map[string]any) map[string]any {
	expanded := map[string]any{}

	for _, key := range slices.Sorted(maps.Keys(settings)) {
		value := settings[key]
		if group, ok := value.(map[string]any); ok {
			value = expandDotted(group)
		}

		parts := strings.Split(key, ".")
		for i := len(parts) - 1; i > 0; i-- {
			value = map[string]any{parts[i]: value}
		}

		mergeTCMSettings(expanded, map[string]any{parts[0]: value}, "")
	}

	return expanded
}

// mergeTCMSettings merges src into dst, whose path is path, dotted: a group
// is merged into the group of the same name key by key, anything else takes
// the place of what dst has. The items of initial-settings.clusters are
// merged by their name, so that the config of the user completes the
// cluster tt declares, such as with its tarantool-connection, rather than
// replace it; an item of another name is added.
func mergeTCMSettings(dst, src map[string]any, path string) {
	for _, key := range slices.Sorted(maps.Keys(src)) {
		value := src[key]
		keyPath := key

		if path != "" {
			keyPath = path + "." + key
		}

		if keyPath == tcmClustersKey {
			dst[key] = mergeClusters(dst[key], value)

			continue
		}

		dstGroup, dstIsGroup := dst[key].(map[string]any)
		srcGroup, srcIsGroup := value.(map[string]any)

		if dstIsGroup && srcIsGroup {
			mergeTCMSettings(dstGroup, srcGroup, keyPath)

			continue
		}

		dst[key] = value
	}
}

// mergeClusters merges the clusters src of initial-settings.clusters into
// dst by their names (mergeTCMSettings). Anything but two lists is src.
func mergeClusters(dst, src any) any {
	dstList, dstIsList := dst.([]any)
	srcList, srcIsList := src.([]any)

	if !dstIsList || !srcIsList {
		return src
	}

	merged := slices.Clone(dstList)

	for _, item := range srcList {
		srcCluster, isGroup := item.(map[string]any)
		if !isGroup {
			merged = append(merged, item)

			continue
		}

		index := slices.IndexFunc(merged, func(existing any) bool {
			group, ok := existing.(map[string]any)

			return ok && group["name"] != nil && group["name"] == srcCluster["name"]
		})
		if index < 0 {
			merged = append(merged, srcCluster)

			continue
		}

		dstCluster, _ := merged[index].(map[string]any)
		mergeTCMSettings(dstCluster, expandDotted(srcCluster), "")
	}

	return merged
}

// settingAt returns the value of the dotted key in settings, and whether it
// is there.
func settingAt(settings map[string]any, key string) (any, bool) {
	var value any = settings

	for part := range strings.SplitSeq(key, ".") {
		group, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}

		value, ok = group[part]
		if !ok {
			return nil, false
		}
	}

	return value, true
}

// tcmVariable returns the TCM_ variable of the config key from the env of
// the record of inst, else from the environment of tt, which TCM inherits:
// TCM ranks both above its config file.
func tcmVariable(inst *InstanceCtx, key string) (string, bool) {
	name := "TCM_" + strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(key))

	if value, ok := inst.Env[name]; ok {
		return value, true
	}

	return os.LookupEnv(name)
}

// tcmURL returns the address of the web interface of TCM with settings,
// http.host, http.port and http.tls.enabled, as TCM takes them, the
// variables first. A host that listens on every interface is reached on the
// loopback.
func tcmURL(inst *InstanceCtx, settings map[string]any) string {
	setting := func(key string) string {
		if value, ok := tcmVariable(inst, key); ok {
			return value
		}

		value, ok := settingAt(settings, key)
		if !ok || value == nil {
			return ""
		}

		return fmt.Sprint(value)
	}

	host := setting("http.host")

	switch host {
	case "", "0.0.0.0":
		host = tcmDefaultHost
	case "::", "[::]":
		host = "::1"
	}

	port := setting("http.port")
	if port == "" {
		port = strconv.Itoa(tcmDefaultPort)
	}

	scheme := "http"
	if enabled, _ := strconv.ParseBool(setting("http.tls.enabled")); enabled {
		scheme = "https"
	}

	return scheme + "://" + net.JoinHostPort(strings.Trim(host, "[]"), port)
}

// tcmPasswordSource tells where the password of the first login of the admin
// user of TCM inst comes from, settings being its config: the file tt gives
// it from, or, for a password the user set otherwise, where it is. The
// variable of the record or of the environment of tt ranks first, as TCM
// ranks it above its config, then the config of the user, then
// password_file of the record, then the file tt generates.
func tcmPasswordSource(inst *InstanceCtx, settings map[string]any) (string, string) {
	if _, ok := inst.Env[tcmPasswordEnv]; ok {
		return "", tcmPasswordEnv + " in the env of the record"
	}

	if _, ok := os.LookupEnv(tcmPasswordEnv); ok {
		return "", tcmPasswordEnv + " of the environment of tt"
	}

	if value, ok := settingAt(settings, tcmPasswordKey); ok && value != nil && value != "" {
		return "", tcmPasswordKey + " of " + inst.TCM.Overlay
	}

	if inst.TCM.PasswordFile != "" {
		return inst.TCM.PasswordFile, ""
	}

	return inst.TCM.SecretFile, ""
}

// tcmConfigHeader starts tcm.yaml.
const tcmConfigHeader = "# The config of TCM of %s, written by tt instance start anew at every\n" +
	"# start: an edit here is lost. Settings of your own go to the file the\n" +
	"# config field of the record of TCM names, which is merged over this one.\n"

// writeTCMConfig writes settings, the config of TCM inst, into its config
// file, whole, with the mode 0600: it may hold the credentials of the config
// storage.
func writeTCMConfig(inst *InstanceCtx, settings map[string]any) error {
	var data bytes.Buffer

	fmt.Fprintf(&data, tcmConfigHeader, GetAppInstanceName(*inst))

	encoder := yaml.NewEncoder(&data)
	encoder.SetIndent(tcmConfigIndent)

	err := errors.Join(encoder.Encode(settings), encoder.Close())
	if err != nil {
		return fmt.Errorf("encoding the config of TCM: %w", err)
	}

	return writeFileWhole(inst.TCM.ConfigFile, data.Bytes(), tcmSecretMode)
}

// tcmConfigIndent is the indent of tcm.yaml.
const tcmConfigIndent = 2

// writeFileWhole writes data into the file at path with mode, through a
// file of its own in the same directory renamed over it, so that a reader
// finds the old file or the new one.
func writeFileWhole(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("cannot write %s: %w", path, err)
	}

	_, writeErr := file.Write(data)

	err = errors.Join(writeErr, file.Chmod(mode), file.Close())
	if err == nil {
		err = os.Rename(file.Name(), path)
	}

	if err != nil {
		_ = os.Remove(file.Name())

		return fmt.Errorf("cannot write %s: %w", path, err)
	}

	return nil
}

// ensureSecret makes sure the file at path holds a password: one there is
// kept, so that TCM gets the same password at every start; without one a
// new password is generated into it, with the mode 0600.
func ensureSecret(path string) error {
	_, err := readSecretFile(path)
	if err == nil {
		return nil
	}

	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w %s: %w", errTCMSecret, path, err)
	}

	password, err := generatePassword()
	if err != nil {
		return err
	}

	return writeFileWhole(path, []byte(password+"\n"), tcmSecretMode)
}

// passwordAlphabet is what a generated password is made of.
const passwordAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// generatePassword returns a random password of tcmPasswordLength letters
// and digits, with a lower case letter, an upper case letter and a digit
// among them, for a password policy that asks for them.
func generatePassword() (string, error) {
	limit := big.NewInt(int64(len(passwordAlphabet)))

	for {
		password := make([]byte, tcmPasswordLength)

		for position := range password {
			index, err := rand.Int(rand.Reader, limit)
			if err != nil {
				return "", fmt.Errorf("cannot generate the password of TCM: %w", err)
			}

			password[position] = passwordAlphabet[index.Int64()]
		}

		text := string(password)
		if strings.ContainsAny(text, passwordAlphabet[:26]) &&
			strings.ContainsAny(text, passwordAlphabet[26:52]) &&
			strings.ContainsAny(text, passwordAlphabet[52:]) {
			return text, nil
		}
	}
}

// readSecretFile returns the password the file at path holds: its text
// without the line break at its end. An empty file is an error.
func readSecretFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err //nolint:wrapcheck // The callers name the file.
	}

	password := strings.TrimRight(string(data), "\r\n")
	if password == "" {
		return "", errEmptySecret
	}

	return password, nil
}

// tcmAnswers reports whether TCM answers on url within timeout: any HTTP
// response will do, since its documentation names no health URL, and its
// pages but the login one ask for a session. A certificate of a TCM over
// TLS is not checked: it is asked whether it runs, and nothing is sent to it.
func tcmAnswers(url string, timeout time.Duration) bool {
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: nil,
			//nolint:gosec // G402: only whether TCM answers is asked, nothing is sent.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: timeout,
	}

	defer client.CloseIdleConnections()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}

	response, err := client.Do(request)
	if err != nil {
		return false
	}

	_ = response.Body.Close()

	return true
}
