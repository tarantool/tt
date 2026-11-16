package running

import (
	"encoding/json"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/internal/instances"
)

// tcmInst is the name of the record of TCM of the fixtures.
const tcmInst = "tcm"

// eeManifest is the manifest of the fixture on Tarantool Enterprise.
const eeManifest = "manifest_version = \"0.1\"\n\n[package]\nname = \"myapp\"\n\n" +
	"[platform]\ntarantool = \">=3.0.0[ee]\"\ntt = \">=3.0.0\"\n"

// tcmFiles returns the files of an application of Tarantool Enterprise
// whose default template runs TCM with the fields record, a YAML flow
// mapping, and has config as its config.yaml, none when it is empty.
func tcmFiles(record, config string) map[string]string {
	files := map[string]string{
		"app.manifest.toml":                 eeManifest,
		".templates/default/instances.yaml": tcmInst + ": " + record + "\n",
	}

	if config != "" {
		files[".templates/default/config.yaml"] = config
	}

	return files
}

// tcmFilesWith is tcmFiles without config.yaml, with extra files on top.
func tcmFilesWith(record string, extra map[string]string) map[string]string {
	files := tcmFiles(record, "")
	maps.Copy(files, extra)

	return files
}

// stringsAt returns the list of strings at key of fields.
func stringsAt(t *testing.T, fields map[string]any, key string) []string {
	t.Helper()

	list, ok := fields[key].([]string)
	require.True(t, ok, key)
	require.NotEmpty(t, list, key)

	return list
}

// tcmCtxOf lays the files out as a project and returns the context of TCM.
func tcmCtxOf(t *testing.T, files map[string]string) (appFixture, InstanceCtx) {
	t.Helper()

	fixture := newAppFixture(t, app.ScopeProject, files)

	return fixture, fixture.instanceCtxOf(t, tcmInst)
}

// group returns the group at the dotted key of settings.
func group(t *testing.T, settings map[string]any, key string) map[string]any {
	t.Helper()

	value, ok := settingAt(settings, key)
	require.True(t, ok, key)

	fields, ok := value.(map[string]any)
	require.True(t, ok, key)

	return fields
}

// firstCluster returns the first item of initial-settings.clusters.
func firstCluster(t *testing.T, settings map[string]any) map[string]any {
	t.Helper()

	value, ok := settingAt(settings, tcmClustersKey)
	require.True(t, ok)

	clusters, ok := value.([]any)
	require.True(t, ok)
	require.NotEmpty(t, clusters)

	cluster, ok := clusters[0].(map[string]any)
	require.True(t, ok)

	return cluster
}

func TestNewInstanceCtxTCM(t *testing.T) {
	for _, appScope := range allScopes {
		t.Run(string(appScope), func(t *testing.T) {
			fixture := newAppFixture(t, appScope, tcmFiles(
				"{kind: tcm, http: 127.0.0.1:18080, config: tcm.yaml, "+
					"password_file: /secrets/admin, env: {TCM_MODE: test}}", ""))
			inst := fixture.instanceCtxOf(t, tcmInst)
			pair := filepath.Join(inst.AppDir, ".templates", "default")

			assert.True(t, IsTCM(&inst))
			assert.True(t, IsExternal(&inst), "TCM runs as an external process does")
			assert.Equal(t, instances.KindTCM, inst.Kind)

			data := filepath.Join(fixture.root, "var", "lib", fixtureApp, tcmInst)
			run := filepath.Join(fixture.root, "var", "run", fixtureApp, tcmInst)

			assert.Equal(t, data, inst.DataDir)
			assert.Equal(t, data, inst.Workdir)
			assert.Equal(t, filepath.Join(run, "process.pid"), inst.ProcessPIDFile)
			assert.Empty(t, inst.ConsoleSocket)
			assert.Empty(t, inst.ClusterConfigPath)
			assert.Equal(t, map[string]string{"TCM_MODE": "test"}, inst.Env)

			assert.Equal(t, TCMCtx{
				HTTP:         "127.0.0.1:18080",
				Overlay:      filepath.Join(pair, "tcm.yaml"),
				PasswordFile: "/secrets/admin",
				PairConfig:   "",
				ConfigFile:   filepath.Join(run, "tcm.yaml"),
				SecretFile:   filepath.Join(data, "admin.secret"),
				Access:       TCMAccess{},
			}, inst.TCM)

			err := RequireTarantool(&inst)
			require.ErrorIs(t, err, ErrNotTarantool)
			assert.ErrorContains(t, err, "myapp:tcm is TCM (kind: tcm)")
		})
	}
}

func TestTCMSettingsEmbedded(t *testing.T) {
	_, inst := tcmCtxOf(t, tcmFiles("{kind: tcm}",
		"groups: {g: {replicasets: {r: {instances: {s: {}}}}}}\n"))

	dry, access, err := tcmSettings(&inst, false)
	require.NoError(t, err)
	assert.NotContains(t, group(t, dry, "storage.etcd"), "endpoints",
		"no ports are chosen without prepare")
	assert.Contains(t, access.Storage, "on ports tt chooses at its first start")
	assert.NoFileExists(t, filepath.Join(inst.DataDir, tcmEtcdStateFileName))

	require.NoError(t, os.MkdirAll(inst.DataDir, 0o700))

	settings, access, err := tcmSettings(&inst, true)
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"host": "127.0.0.1", "port": 8080}, group(t, settings, "http"))
	assert.Equal(t, "http://127.0.0.1:8080", access.URL)
	assert.Equal(t, TCMAdminUser, access.User)

	etcd := group(t, settings, "storage.etcd")
	embed := group(t, settings, "storage.etcd.embed")
	client := stringsAt(t, etcd, "endpoints")[0]
	peer := stringsAt(t, embed, "peer-endpoints")[0]

	assert.Equal(t, "/tcm/myapp", etcd["prefix"])
	assert.Equal(t, true, embed["enabled"])
	assert.Equal(t, filepath.Join(inst.DataDir, "etcd"), embed["workdir"])
	assert.Equal(t, []string{client}, embed["endpoints"])
	assert.Equal(t, "tcm="+peer, embed["initial-cluster"])
	assert.NotEqual(t, client, peer)
	assert.True(t, strings.HasPrefix(client, "http://127.0.0.1:"))

	state := filepath.Join(inst.DataDir, tcmEtcdStateFileName)
	info, err := os.Stat(state)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	cluster := firstCluster(t, settings)
	assert.Equal(t, tcmDefaultClusterID, cluster["id"])
	assert.Equal(t, fixtureApp, cluster["name"])
	assert.Equal(t, map[string]any{
		"provider": "etcd",
		"etcd-connection": map[string]any{
			"endpoints": []string{client}, "prefix": "/myapp",
		},
	}, cluster["storage-connection"])

	assert.Contains(t, access.Storage, "the embedded etcd of TCM, "+client)
	assert.Contains(t, access.Note, "keeps its cluster config in the file")
	assert.Contains(t, access.Note, "tt cluster publish myapp")

	// The addresses are chosen once: etcd keeps its peer address in its data.
	again, _, err := tcmSettings(&inst, true)
	require.NoError(t, err)
	assert.Equal(t, settings, again)
}

func TestTCMSettingsEtcd(t *testing.T) {
	_, inst := tcmCtxOf(t, tcmFiles("{kind: tcm, http: \"0.0.0.0:9000\"}",
		"config:\n  etcd:\n    endpoints: [https://etcd-1:2379, https://etcd-2:2379]\n"+
			"    prefix: /myapp\n    username: client\n    password: secret\n"+
			"    ssl:\n      ca_file: /certs/ca.pem\n"))

	settings, access, err := tcmSettings(&inst, false)
	require.NoError(t, err)

	tlsGroup := map[string]any{
		"enabled": true, "insecure-skip-verify": false, "trusted-ca-file": "/certs/ca.pem",
	}

	assert.Equal(t, map[string]any{
		"provider": "etcd",
		"etcd": map[string]any{
			"endpoints": []string{"https://etcd-1:2379", "https://etcd-2:2379"},
			"prefix":    "/tcm/myapp",
			"username":  "client",
			"password":  "secret",
			"tls":       tlsGroup,
		},
	}, settings["storage"])

	assert.Equal(t, map[string]any{
		"provider": "etcd",
		"etcd-connection": map[string]any{
			"endpoints": []string{"https://etcd-1:2379", "https://etcd-2:2379"},
			"prefix":    "/myapp",
			"username":  "client",
			"password":  "secret",
			"tls":       tlsGroup,
		},
	}, firstCluster(t, settings)["storage-connection"])

	assert.Empty(t, access.Note)
	assert.Equal(t, `etcd https://etcd-1:2379,https://etcd-2:2379, prefix "/tcm/myapp", `+
		`the config storage of the application`, access.Storage)
	assert.Equal(t, "http://127.0.0.1:9000", access.URL,
		"a TCM on every interface is reached on the loopback")
	assert.NoDirExists(t, inst.DataDir, "nothing is written without prepare")
}

func TestTCMSettingsTarantoolStorage(t *testing.T) {
	_, inst := tcmCtxOf(t, tcmFiles("{kind: tcm}",
		"config:\n  storage:\n    prefix: /myapp\n    endpoints:\n"+
			"      - {uri: 127.0.0.1:4401, login: sampleuser, password: '123'}\n"+
			"      - {uri: 127.0.0.1:4402, login: sampleuser, password: '123'}\n"))

	settings, _, err := tcmSettings(&inst, false)
	require.NoError(t, err)

	assert.Equal(t, map[string]any{
		"provider": "tarantool",
		"tarantool": map[string]any{
			"addrs":    []string{"127.0.0.1:4401", "127.0.0.1:4402"},
			"prefix":   "/tcm/myapp",
			"username": "sampleuser",
			"password": "123",
		},
	}, settings["storage"])

	assert.Equal(t, map[string]any{
		"provider": "tarantool",
		"tarantool-connection": map[string]any{
			"endpoints": []string{"127.0.0.1:4401", "127.0.0.1:4402"},
			"prefix":    "/myapp",
			"username":  "sampleuser",
			"password":  "123",
		},
	}, firstCluster(t, settings)["storage-connection"])
}

// tcmOverlay is a config of TCM of the user: groups, dotted keys, and the
// cluster of the application completed.
const tcmOverlay = `mode: development
http:
  port: 9443
  tls: {enabled: true, cert-file: /c.pem, key-file: /k.pem}
storage.etcd.embed.log-level: debug
initial-settings:
  clusters:
    - name: myapp
      tarantool-connection: {username: admin, password: pass}
      storage-connection.etcd-connection.dial-timeout: 5s
    - name: other
`

func TestTCMSettingsOverlay(t *testing.T) {
	_, inst := tcmCtxOf(t, tcmFilesWith("{kind: tcm, config: tcm.yaml}",
		map[string]string{".templates/default/tcm.yaml": tcmOverlay}))

	settings, access, err := tcmSettings(&inst, false)
	require.NoError(t, err)

	assert.Equal(t, "development", settings["mode"])
	assert.Equal(t, "127.0.0.1", group(t, settings, "http")["host"], "kept from tt")
	assert.Equal(t, 9443, group(t, settings, "http")["port"])
	assert.Equal(t, "https://127.0.0.1:9443", access.URL)

	embed := group(t, settings, "storage.etcd.embed")
	assert.Equal(t, "debug", embed["log-level"], "a dotted key is a group")
	assert.Equal(t, true, embed["enabled"], "the group of tt is merged, not replaced")

	value, _ := settingAt(settings, tcmClustersKey)
	clusters, ok := value.([]any)
	require.True(t, ok)
	require.Len(t, clusters, 2)

	mine := firstCluster(t, settings)
	assert.Equal(t, tcmDefaultClusterID, mine["id"], "the cluster of tt is completed")
	assert.Equal(t, map[string]any{"username": "admin", "password": "pass"},
		mine["tarantool-connection"])
	assert.Equal(t, "5s", group(t, mine, "storage-connection.etcd-connection")["dial-timeout"])
	assert.Equal(t, "/myapp", group(t, mine, "storage-connection.etcd-connection")["prefix"])
	assert.Equal(t, map[string]any{"name": "other"}, clusters[1])
}

func TestTCMOverlayErrors(t *testing.T) {
	_, inst := tcmCtxOf(t, tcmFilesWith("{kind: tcm, config: tcm.yaml}",
		map[string]string{".templates/default/tcm.yaml": "http: [not, a, group\n"}))

	_, _, err := tcmSettings(&inst, false)
	require.ErrorIs(t, err, errTCMConfig)

	_, inst = tcmCtxOf(t, tcmFiles("{kind: tcm, config: missing.yaml}", ""))

	_, err = checkTCM(&inst)
	require.ErrorIs(t, err, errTCMConfig)
	require.ErrorContains(t, err, "missing.yaml (config)")
}

func TestTCMPasswordSource(t *testing.T) {
	t.Setenv(tcmPasswordEnv, "")
	require.NoError(t, os.Unsetenv(tcmPasswordEnv))

	files := tcmFiles("{kind: tcm}", "")
	_, inst := tcmCtxOf(t, files)

	file, from := tcmPasswordSource(&inst, map[string]any{})
	assert.Equal(t, inst.TCM.SecretFile, file, "tt generates it by default")
	assert.Empty(t, from)

	inst.TCM.PasswordFile = "/run/secrets/tcm"
	file, _ = tcmPasswordSource(&inst, map[string]any{})
	assert.Equal(t, "/run/secrets/tcm", file, "password_file of the record wins")

	overlay := map[string]any{"security": map[string]any{"bootstrap-password": "x"}}

	inst.TCM.Overlay = "/pair/tcm.yaml"

	file, from = tcmPasswordSource(&inst, overlay)
	assert.Empty(t, file, "the config of the user wins over the files")
	assert.Equal(t, "security.bootstrap-password of /pair/tcm.yaml", from)

	t.Setenv(tcmPasswordEnv, "y")

	file, from = tcmPasswordSource(&inst, overlay)
	assert.Empty(t, file)
	assert.Equal(t, tcmPasswordEnv+" of the environment of tt", from,
		"TCM ranks the environment above its config")

	inst.Env = map[string]string{tcmPasswordEnv: "z"}
	_, from = tcmPasswordSource(&inst, overlay)
	assert.Equal(t, tcmPasswordEnv+" in the env of the record", from)
}

// tcmProject lays a project with TCM out, with an executable as its tcm.
func tcmProject(t *testing.T, files map[string]string) (appFixture, InstanceCtx) {
	t.Helper()

	fixture, inst := tcmCtxOf(t, files)
	writeExecutable(t, filepath.Join(fixture.root, "bin", "tcm"))

	return fixture, inst
}

func TestTCMSpec(t *testing.T) {
	require.NoError(t, os.Unsetenv(tcmPasswordEnv))

	fixture, inst := tcmProject(t, tcmFiles("{kind: tcm, http: 127.0.0.1:18090}", ""))

	launch, err := ResolveLaunch(&inst)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(fixture.root, "bin", "tcm"), launch.Spec.Path)
	assert.Equal(t, []string{"-c=" + inst.TCM.ConfigFile}, launch.Spec.Args)
	assert.Equal(t, inst.DataDir, launch.Spec.Dir)
	assert.True(t, launch.Spec.ProcessGroup)

	access := launch.Instance.TCM.Access
	assert.Equal(t, "http://127.0.0.1:18090", access.URL)
	assert.Equal(t, inst.TCM.SecretFile, access.PasswordFile)
	assert.Equal(t, "http://127.0.0.1:18090 (user admin, password in "+inst.TCM.SecretFile+")",
		access.Line())
	assert.Equal(t, map[string]string{tcmPasswordEnv: inst.TCM.SecretFile},
		launch.Spec.EnvFiles)

	for _, path := range []string{inst.TCM.ConfigFile, inst.TCM.SecretFile} {
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), path)
	}

	password, err := readSecretFile(inst.TCM.SecretFile)
	require.NoError(t, err)
	assert.Len(t, password, tcmPasswordLength)

	config, err := os.ReadFile(inst.TCM.ConfigFile)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(config), "# The config of TCM of myapp:tcm"))
	assert.Contains(t, string(config), "http:\n  host: 127.0.0.1\n  port: 18090\n")
	assert.NotContains(t, string(config), password)

	// The watchdog reads the password at every start, and the launch keeps
	// only where it is.
	spec, err := launch.Spec.supervisorSpec(nil, nil)
	require.NoError(t, err)
	assert.Contains(t, spec.Env, tcmPasswordEnv+"="+password)

	//nolint:musttag // The context of the instance is encoded by its field names.
	encoded, err := json.Marshal(&launch)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), password)

	// A second start keeps the password.
	again, err := ResolveLaunch(&inst)
	require.NoError(t, err)

	kept, err := readSecretFile(again.Instance.TCM.SecretFile)
	require.NoError(t, err)
	assert.Equal(t, password, kept)

	// A password file that is gone stops the start of the watchdog.
	require.NoError(t, os.Remove(inst.TCM.SecretFile))

	_, err = launch.Spec.supervisorSpec(nil, nil)
	require.ErrorContains(t, err, "the value of "+tcmPasswordEnv)
}

func TestTCMSpecPasswordFile(t *testing.T) {
	require.NoError(t, os.Unsetenv(tcmPasswordEnv))

	secret := filepath.Join(t.TempDir(), "admin")
	require.NoError(t, os.WriteFile(secret, []byte("from-the-user\n"), 0o600))

	_, inst := tcmProject(t, tcmFiles("{kind: tcm, password_file: "+secret+"}", ""))

	launch, err := ResolveLaunch(&inst)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{tcmPasswordEnv: secret}, launch.Spec.EnvFiles)
	assert.NoFileExists(t, inst.TCM.SecretFile, "tt generates none")

	spec, err := launch.Spec.supervisorSpec(nil, nil)
	require.NoError(t, err)
	assert.Contains(t, spec.Env, tcmPasswordEnv+"=from-the-user")

	require.NoError(t, os.WriteFile(secret, nil, 0o600))

	_, err = checkTCM(&inst)
	require.ErrorIs(t, err, errTCMSecret)
}

func TestTCMSpecPasswordFromEnv(t *testing.T) {
	_, inst := tcmProject(t, tcmFiles(
		"{kind: tcm, env: {TCM_SECURITY_BOOTSTRAP_PASSWORD: mine}}", ""))

	launch, err := ResolveLaunch(&inst)
	require.NoError(t, err)
	assert.Nil(t, launch.Spec.EnvFiles)
	assert.NoFileExists(t, inst.TCM.SecretFile)
	assert.Equal(t, "http://127.0.0.1:8080 (user admin, password from "+tcmPasswordEnv+
		" in the env of the record)", launch.Instance.TCM.Access.Line())
}

func TestTCMCommunityRefused(t *testing.T) {
	files := tcmFilesWith("{kind: tcm}", map[string]string{
		"app.manifest.toml": fixtureManifest,
	})

	_, inst := tcmProject(t, files)

	err := checkStart(&inst)
	require.ErrorIs(t, err, manifest.ErrTCMCommunity)
	require.ErrorContains(t, err, `tarantool = ">=3.0.0[ee]"`)

	_, err = ResolveLaunch(&inst)
	require.ErrorIs(t, err, manifest.ErrTCMCommunity)

	// Without a manifest nothing says the edition: a warning.
	delete(files, "app.manifest.toml")

	_, inst = tcmProject(t, files)

	warning, err := CheckTCMEdition(&inst)
	require.NoError(t, err)
	assert.Contains(t, warning, "there is no app.manifest.toml")
}

func TestFindTCMBinary(t *testing.T) {
	for _, appScope := range allScopes {
		t.Run(string(appScope), func(t *testing.T) {
			fixture := newAppFixture(t, appScope, tcmFilesWith("{kind: tcm}",
				map[string]string{"app.manifest.toml": eeManifest + "tcm = \">=1.3\"\n"}))
			inst := fixture.instanceCtxOf(t, tcmInst)

			_, err := ResolveBinary(&inst)
			require.ErrorIs(t, err, errNoTCM)
			require.ErrorContains(t, err, "install it with tt binary install tcm <version>")
			require.ErrorContains(t, err, `[platform].tcm allows, ">=1.3"`)

			if appScope != app.ScopeProject {
				require.ErrorContains(t, err, "--scope "+string(appScope))
			}

			active := filepath.Join(fixture.root, "bin", "tcm")
			writeExecutable(t, active)

			path, err := ResolveBinary(&inst)
			require.NoError(t, err)
			assert.Equal(t, active, path)

			// The TCM the lock bundles, as tt package install puts it, wins.
			lock := fixtureLock + "bundled_tcm_version = \"1.3.1\"\n"
			require.NoError(t, os.WriteFile(filepath.Join(inst.AppDir, "app.manifest.lock"),
				[]byte(lock), 0o644))

			bundled := filepath.Join(fixture.root, "versions", "tcm", "tcm_1.3.1", "tcm")
			writeExecutable(t, bundled)

			path, err = ResolveBinary(&inst)
			require.NoError(t, err)
			assert.Equal(t, bundled, path)
		})
	}
}

func TestTCMAnswers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	assert.True(t, tcmAnswers(server.URL, probeTimeout), "any response is TCM up")

	ports, err := freePorts(1)
	require.NoError(t, err)

	closed := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[0]))

	assert.False(t, tcmAnswers(closed, probeTimeout))
}

func TestGeneratePassword(t *testing.T) {
	seen := map[string]bool{}

	for range 20 {
		password, err := generatePassword()
		require.NoError(t, err)
		assert.Len(t, password, tcmPasswordLength)
		assert.True(t, strings.ContainsAny(password, "0123456789"))
		assert.False(t, seen[password])

		seen[password] = true
	}
}
