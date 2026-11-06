# Написание модуля tt

Модуль tt - это Go-модуль, скомпилированный в
[дистрибутив tt](build-distribution.md): его пакет экспортирует конструктор,
возвращающий добавляемые им команды. Он построен на
[SDK tt](https://github.com/tarantool/tt/blob/v3/sdk/README.md) и больше
ничего из tt не импортирует. На этой странице с нуля пишется модуль `tt
probe`, который выводит список инстансов приложения, отправляет ping инстансу
и делает на нём снапшот, а затем тестируется и встраивается в tt. О том,
как добавить команду отдельным исполняемым файлом, который tt запускает без
пересборки, см. [Внешние модули](external-modules.md).

SDK находится в статусе alpha: его API может измениться в любом релизе tt.

## Настройка Go-модуля {#set-up-the-go-module}

```sh
mkdir tt-probe && cd tt-probe
go mod init example.com/tt-probe
go get github.com/tarantool/tt/sdk@9997ea8b \
    github.com/tarantool/tt/sdk/connect@9997ea8b \
    github.com/tarantool/tt/sdk/dial@9997ea8b
go get github.com/tarantool/go-tarantool/v3 github.com/stretchr/testify
```

У модулей SDK пока нет версий: у tt нет тегов `sdk/...`, а его собственные
теги вроде `v3.0.0-alpha.2` версионируют ядро, `github.com/tarantool/tt/v3`,
и никакой другой модуль. Модули SDK подключают по коммиту tt, из которого
Go формирует псевдоверсию:

```text
require (
	github.com/tarantool/tt/sdk v0.0.0-20261105160839-9997ea8b6840
	github.com/tarantool/tt/sdk/connect v0.0.0-20261105160839-9997ea8b6840
	github.com/tarantool/tt/sdk/dial v0.0.0-20261105160839-9997ea8b6840
)
```

`9997ea8b` - это коммит `v3.0.0-alpha.2` (`git rev-parse
'v3.0.0-alpha.2^{commit}'` в клоне tt); берут коммит ядра, под которое
строится модуль. Все модули SDK подключают по одному и тому же коммиту,
включая `sdk/connect`, даже если модуль его не импортирует: в `go.mod`
модуля `sdk` он прописан на `v0.0.0-0` - плейсхолдер, который разрешают
только директивы `replace` в репозитории самого tt, а `go get` для одного
лишь `sdk` падает с ошибкой `unknown revision sdk/connect/v0.0.0-0`. В
`sdk/cluster` есть два таких плейсхолдера: для `sdk/connect` и `sdk/dial`.

Сборка дистрибутива эти версии не использует: она собирает каждый модуль на
SDK по коммиту его ядра, что бы ни требовал `go.mod` самого модуля. По этим
версиям запускается `go test` модуля, поэтому их держат на коммите ядра:
когда ядро обновляется, подтягивают модули SDK через `go get` на его новом
коммите и выполняют `go mod tidy`.

### Разработка против чекаута tt {#develop-against-a-checkout-of-tt}

Чтобы собрать модуль на изменениях в SDK, которые ещё не запушены, модули
SDK из локального чекаута tt добавляют в workspace Go. Файл `go.work` не
коммитят в git:

```sh
go work init . ../tt/sdk ../tt/sdk/connect ../tt/sdk/dial
go test ./...
```

и собирают дистрибутив на том же чекауте командой `tt binary build tt --core
../tt` (см. [Сборка на другом ядре](build-distribution.md#building-on-another-core)).
Сборка дистрибутива игнорирует `go.work`, а также директивы `replace` из
`go.mod` модуля: Go применяет их только из главного модуля.

## Написание конструктора {#write-the-constructor}

`probe.go`:

```go
// Package probe is a tt module: tt probe lists the instances of an
// application, pings an instance and makes a snapshot on it.
package probe

import (
	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk"
)

// New is the constructor of the module. It declares the commands and
// nothing else: the services other than Log are not ready yet.
func New(services sdk.Services) []sdk.Mount {
	group := &cobra.Command{
		Use:   "probe",
		Short: "Look at the instances of an application",
	}

	group.AddCommand(
		newInstancesCmd(services),
		newPingCmd(services),
		newSnapshotCmd(services),
	)

	return []sdk.Mount{{Path: "", Cmd: group}}
}
```

`New` представляет собой `sdk.Constructor`, `func(sdk.Services)
[]sdk.Mount`; сборка дистрибутива ищет его под этим именем в корневом
пакете модуля, если в его манифесте не указано иное. tt вызывает его один
раз на процесс, до того как tt сконфигурирован: из всех сервисов здесь
разрешено вызывать только `Log`, а остальные вызывают panic с указанием
модуля и метода. Команды используют сервисы в своих хуках, которые
выполняются позже. Конструктор объявляет команды и их флаги и ничего
больше: он не читает ни файлы, ни переменные окружения.

`Mount` - это команда и путь к её родительской команде: `""` означает
корень, а `"replicaset"` помещает команду под `tt replicaset`. Группа по
пути, которую не предоставляет ни один модуль, создаётся автоматически, без
описания. Каждая команда создаётся заново, без родителя; ядро забирает её
под своё управление сразу после возврата из конструктора.

## Список инстансов приложения {#list-the-instances-of-an-application}

`instances.go`:

```go
package probe

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/output"
)

// instancesResult is the result of tt probe instances. Its fields are what
// -o json and -o yaml print.
type instancesResult struct {
	App       string   `json:"app" yaml:"app"`
	Instances []string `json:"instances" yaml:"instances"`
}

// Human prints one instance per line.
func (r instancesResult) Human(w io.Writer) error {
	for _, name := range r.Instances {
		_, err := fmt.Fprintln(w, name)
		if err != nil {
			return err
		}
	}

	return nil
}

func newInstancesCmd(services sdk.Services) *cobra.Command {
	var lookup sdk.AppLookup

	cmd := &cobra.Command{
		Use:   "instances APP",
		Short: "List the instances of an application",
		Args:  cobra.ExactArgs(1),
	}

	format := output.BindFormat(cmd.Flags(), output.FormatHuman,
		output.FormatHuman, output.FormatJSON, output.FormatYAML)
	sdk.AddAppFlags(cmd, &lookup)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		app, instance := sdk.SplitInstance(args[0])
		if instance != "" {
			return sdk.Usagef("%q names an instance: give the application only", args[0])
		}

		cfg, err := services.ClusterConfig(cmd.Context(),
			sdk.AppSource(app).WithLookup(lookup))
		if err != nil {
			return err
		}

		names, err := sdk.Instances(cfg.Config)
		if err != nil {
			return err
		}

		services.Log().Debug("Read the cluster configuration", "app", app, "dir", cfg.Dir)

		printer, err := services.Streams().Printer(format.Format())
		if err != nil {
			return err
		}

		return printer.Emit(instancesResult{App: app, Instances: names})
	}

	return cmd
}
```

- `sdk.AddAppFlags` объявляет флаги `--scope` и `--template` со справкой и
  автодополнением, аналогичными командам для инстансов в tt, а
  `sdk.AppSource(app).WithLookup(lookup)` находит приложение тем же
  способом: проект, в котором запущен tt, если имя совпадает, иначе scope
  системы. `sdk.SplitInstance` разделяет `APP:INSTANCE`.
- `Services.ClusterConfig` возвращает кластерную конфигурацию источника так,
  как её видит Tarantool 3, вместе с каталогом, относительно которого
  отсчитываются относительные пути в ней (`Dir`); приложение без
  конфигурации оборачивает `sdk.ErrNotFound`. `sdk.Instances` перечисляет
  его инстансы, а `sdk.InstanceConfig` вычисляет конфигурацию одного из них.
  `sdk.ParseClusterSource` разбирает аргумент, который также может быть
  файлом кластерной конфигурации или URI хранилища конфигурации, как это
  делает `tt cluster show`.
- `output.BindFormat` объявляет флаг `-o/--format` с поддерживаемыми
  командой форматами и форматом по умолчанию. `Services.Streams().Printer`
  выводит результат в stdout: метод `Human` для формата для человека
  (`table`), а само значение - в `json` и `yaml` по тегам `json` и `yaml`,
  поэтому у каждого поля есть оба тега. В stdout попадает только результат;
  логи и приглашения идут в stderr.
- `Services.Log` - логер модуля: записи содержат атрибут `module=probe`, а
  отладочные записи выводятся с ключом `tt -V`. `log.LibraryOf` создаёт
  логер для библиотеки, понижая её записи Info до уровня Debug.
- Ошибку, возвращённую из `RunE`, tt выводит один раз и выставляет код
  завершения. `sdk.Usagef` и `sdk.WithUsage` помечают ошибку в вызове
  команды: tt выводит сообщение, а затем подсказку по использованию
  команды. Ошибка в `Args` или во флагах также выводится вместе со справкой
  по использованию.

## Подключение к инстансу {#connect-to-an-instance}

`target.go`:

```go
package probe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	goconfig "github.com/tarantool/go-config/v2"
	"github.com/tarantool/go-tarantool/v3"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/sdk/dial"
	"github.com/tarantool/tt/sdk/log"
)

// timeout bounds the connection and each request.
const timeout = 3 * time.Second

// connFlags are the flags of a command that connects to an instance.
type connFlags struct {
	username string
	password string
	lookup   sdk.AppLookup
}

// bind declares the flags on cmd.
func (f *connFlags) bind(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVarP(&f.username, "username", "u", "", "the user to connect as")
	flags.StringVarP(&f.password, "password", "p", "", "the password of the user")
	sdk.AddAppFlags(cmd, &f.lookup)
}

// listen is an entry of iproto.listen in the configuration of an instance.
type listen struct {
	URI    string            `yaml:"uri"`
	Params map[string]string `yaml:"params"`
}

// connect connects to target: a URI, with or without credentials, or
// APP:INSTANCE. A failure to connect is a failure of the system: tt exits 2.
func (f *connFlags) connect(
	ctx context.Context, services sdk.Services, target string,
) (*tarantool.Connection, error) {
	opts, err := f.dialOpts(ctx, services, target)
	if err != nil {
		return nil, err
	}

	dialer, err := dial.New(opts)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := tarantool.Connect(ctx, dialer, tarantool.Opts{
		Timeout:    timeout,
		SkipSchema: true,
		Logger:     log.LibraryOf(services.Log(), "go-tarantool"),
	})
	if err != nil {
		return nil, sdk.WithCode(sdk.ExitSystem,
			fmt.Errorf("connecting to %s: %w", log.RedactTarget(target), err))
	}

	return conn, nil
}

// dialOpts returns the options of a dialer to target. The credentials are
// the ones a URI carries, which exclude -u and -p; otherwise -u and -p, and
// an empty one is taken from TT_CLI_USERNAME or TT_CLI_PASSWORD, as tt
// connect takes them.
func (f *connFlags) dialOpts(
	ctx context.Context, services sdk.Services, target string,
) (dial.Opts, error) {
	opts := dial.Opts{Auth: tarantool.AutoAuth, User: f.username, Password: f.password}
	params := map[string]string{}
	uri := target

	switch {
	case connect.IsCredentialsURI(target):
		if f.username != "" || f.password != "" {
			return opts, sdk.Usagef("the URI has credentials: -u and -p are not allowed")
		}

		uri, opts.User, opts.Password = connect.ParseCredentialsURI(target)
	case connect.IsBaseURI(target):
	default:
		entry, err := f.instanceListen(ctx, services, target)
		if err != nil {
			return opts, err
		}

		uri, params = entry.URI, entry.Params
	}

	if opts.User == "" {
		opts.User = os.Getenv(connect.TarantoolUsernameEnv)
	}

	if opts.Password == "" {
		opts.Password = os.Getenv(connect.TarantoolPasswordEnv)
	}

	log.RegisterSecret(opts.Password)

	// host:port?transport=ssl&ssl_ca_file=ca.crt
	base, query, err := connect.SplitURIQuery(uri)
	if err != nil {
		return opts, sdk.WithUsage(err)
	}

	for key := range query {
		params[key] = query.Get(key)
	}

	opts.Transport = params["transport"]
	opts.SslKeyFile = params["ssl_key_file"]
	opts.SslCertFile = params["ssl_cert_file"]
	opts.SslCaFile = params["ssl_ca_file"]
	opts.SslCiphers = params["ssl_ciphers"]

	network, address := connect.ParseBaseURI(base)
	opts.Address = network + "://" + address

	return opts, nil
}

// instanceListen returns the first iproto.listen entry of the instance
// target, APP:INSTANCE, names, with its SSL files relative to the
// application directory made absolute. The URI is taken as it is written.
func (f *connFlags) instanceListen(
	ctx context.Context, services sdk.Services, target string,
) (listen, error) {
	app, instance := sdk.SplitInstance(target)
	if instance == "" {
		return listen{}, sdk.Usagef("%q is neither a URI nor APP:INSTANCE",
			log.RedactTarget(target))
	}

	cfg, err := services.ClusterConfig(ctx, sdk.AppSource(app).WithLookup(f.lookup))
	if err != nil {
		return listen{}, err
	}

	instanceCfg, err := sdk.InstanceConfig(cfg.Config, instance)
	if err != nil {
		return listen{}, err
	}

	var entries []listen

	_, err = instanceCfg.Get(goconfig.NewKeyPath("iproto/listen"), &entries)
	if err != nil {
		return listen{}, fmt.Errorf("%s: iproto.listen: %w", target, err)
	}

	if len(entries) == 0 || entries[0].URI == "" {
		return listen{}, fmt.Errorf("%s: iproto.listen has no URI", target)
	}

	entry := entries[0]
	if entry.Params == nil {
		entry.Params = map[string]string{}
	}

	for key, value := range entry.Params {
		if strings.HasPrefix(key, "ssl_") && strings.HasSuffix(key, "_file") &&
			value != "" && !filepath.IsAbs(value) {
			entry.Params[key] = filepath.Join(cfg.Dir, value)
		}
	}

	return entry, nil
}
```

SDK предоставляет составные части, а модуль собирает их воедино, включая
учётные данные:

1. `connect.IsCredentialsURI` определяет URI с учётными данными,
   `user:password@host:port` и его unix-формы, а
   `connect.ParseCredentialsURI` извлекает их. `connect.IsBaseURI` проверяет
   URI без них: `host:port`, `tcp://host:port`, `unix/:path`, `unix://path`
   и путь, начинающийся с `/`, `./`, `../` или `~/`.
2. Всё остальное здесь считается `APP:INSTANCE`: в качестве URI берётся
   первая запись `iproto.listen` инстанса, а `Dir` делает пути к SSL-файлам
   из её параметров абсолютными. В этом примере URI берётся как есть;
   подстановка `{{ instance_name }}` и преобразование относительного пути к
   сокету в абсолютный не выполняются.
3. Приоритет учётных данных определяет сам модуль, в SDK он не зашит. В
   примере логика повторяет `tt connect`: учётные данные в URI исключают
   `-u` и `-p`; иначе используются `-u` и `-p`, а незаполненные значения
   берутся из `TT_CLI_USERNAME` или `TT_CLI_PASSWORD`
   (`connect.TarantoolUsernameEnv`, `connect.TarantoolPasswordEnv`). Описание
   этих переменных в справке команды формирует
   `connect.EnvTarantoolCredentialsHelp`.
4. `connect.SplitURIQuery` отделяет query от URI, где передаются `transport`
   и параметры `ssl_*`, а `connect.ParseBaseURI` возвращает сеть и адрес,
   которые передаются в `dial.Opts.Address` в виде `network://address`.
5. `dial.New` возвращает диалер для `tarantool.Connect` из go-tarantool.
   Если параметр `Transport` пуст, выбирается SSL при наличии любого
   SSL-поля, иначе plain; явный выбор задают значения `"plain"` и `"ssl"`,
   остальное считается ошибкой. `Auth` используется только поверх SSL.
   SSL-диалер представляет собой реализацию TLS на чистом Go, а в сборке с
   тегом `openssl` - реализацию на OpenSSL.
6. `log.RegisterSecret` скрывает пароль во всех записях лога. Результат
   выполнения и приглашения к вводу не относятся к записям лога:
   `log.RedactTarget` скрывает учётные данные цели при её выводе в них и в
   тексте ошибок.

`connect.CreateURIOpts` разбирает URL со схемой и хостом, такой как
`https://user:pass@localhost:2379/prefix?timeout=5`, в структуру
`connect.URIOpts`: эндпоинт, префикс, учётные данные, SSL-файлы и таймаут
(по умолчанию 3 секунды). Функция предназначена для хранилищ конфигурации
etcd и Tarantool, но не для URI iproto, у которых схемы нет;
`connect.MakeURLHelp` формирует текст справки для такого аргумента с URL.
Кластерная конфигурация из хранилища считывается через
`Services.ClusterConfig` с источником `sdk.StorageSource`, который берёт
учётные данные сначала из URI, затем из `sdk.Credentials`, затем из
`TT_CLI_ETCD_USERNAME` и `TT_CLI_ETCD_PASSWORD` (либо `TT_CLI_USERNAME` и
`TT_CLI_PASSWORD` для хранилища конфигурации Tarantool).

## Коды завершения и подтверждения {#exit-codes-and-confirmations}

`ping.go`:

```go
package probe

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/tarantool/go-tarantool/v3"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/sdk/output"
)

// errCancelled is a snapshot the user did not confirm.
var errCancelled = errors.New("cancelled: confirm, or give --force")

// pingResult is the result of tt probe ping.
type pingResult struct {
	Target string `json:"target" yaml:"target"`
	Status string `json:"status" yaml:"status"`
}

// Human prints the status of the instance.
func (r pingResult) Human(w io.Writer) error {
	_, err := fmt.Fprintf(w, "%s: %s\n", r.Target, r.Status)

	return err
}

func newPingCmd(services sdk.Services) *cobra.Command {
	var conn connFlags

	cmd := &cobra.Command{
		Use:   "ping (URI | APP:INSTANCE)",
		Short: "Connect to an instance and print its status",
		Long: "Connect to an instance and print box.info.status.\n\n" +
			"The URI is [user:password@]host:port, a path to a unix socket\n" +
			"or unix/:path, with an optional ?transport=ssl&ssl_ca_file=...\n\n" +
			connect.EnvTarantoolCredentialsHelp,
		Args: cobra.ExactArgs(1),
	}

	conn.bind(cmd)
	format := output.BindFormat(cmd.Flags(), output.FormatHuman,
		output.FormatHuman, output.FormatJSON, output.FormatYAML)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		instance, err := conn.connect(cmd.Context(), services, args[0])
		if err != nil {
			return err
		}
		defer instance.Close()

		var status []string

		err = instance.Do(tarantool.NewEvalRequest("return box.info.status")).
			GetTyped(&status)
		if err != nil {
			return fmt.Errorf("box.info.status: %w", err)
		}

		printer, err := services.Streams().Printer(format.Format())
		if err != nil {
			return err
		}

		return printer.Emit(pingResult{Target: log.RedactTarget(args[0]), Status: status[0]})
	}

	return cmd
}

func newSnapshotCmd(services sdk.Services) *cobra.Command {
	var (
		conn  connFlags
		force bool
	)

	cmd := &cobra.Command{
		Use:   "snapshot (URI | APP:INSTANCE)",
		Short: "Make a snapshot on an instance",
		Args:  cobra.ExactArgs(1),
	}

	conn.bind(cmd)
	cmd.Flags().BoolVarP(&force, "force", "f", false, "do not ask for confirmation")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if !force {
			// Under tt --no-prompt this asks nothing and answers false.
			yes, err := services.Confirm(
				"Make a snapshot on "+log.RedactTarget(args[0])+"?", false)
			if err != nil {
				return err
			}

			if !yes {
				return errCancelled
			}
		}

		instance, err := conn.connect(cmd.Context(), services, args[0])
		if err != nil {
			return err
		}
		defer instance.Close()

		_, err = instance.Do(tarantool.NewEvalRequest("box.snapshot()")).Get()
		if err != nil {
			return fmt.Errorf("box.snapshot: %w", err)
		}

		services.Log().Info("Snapshot made", "target", log.RedactTarget(args[0]))

		return nil
	}

	return cmd
}
```

tt завершается с кодом ошибки, которую возвращает команда (см.
[Коды завершения](../reference/commands/errors.md)): 1 (`sdk.ExitFailure`)
при любой ошибке, 2 (`sdk.ExitSystem`) при системном сбое, 3
(`sdk.ExitPartial`) для операции, завершившейся успешно только на части
целей. Код задаётся через `sdk.WithCode` и `sdk.Errorf`; код 1 никогда не
перекрывает более специфический код глубже в цепочке. Кроме того, tt
завершается с кодом 2 при сетевых ошибках, нехватке прав или места даже без
явного кода; в примере код всё равно выставляется явно, чтобы его видели
тесты.

`Services.Confirm` задаёт вопрос "да/нет" в stderr и читает ответ из
stdin. При флаге `tt --no-prompt` метод ничего не спрашивает и возвращает
значение по умолчанию, поэтому значением по умолчанию выбирают безопасный
ответ. Флаг вроде `--force` позволяет скриптам продолжать работу без
вопросов.

`Services.Exit` выводит сообщение об ошибке и завершает tt с соответствующим
кодом; это нужно для кода, который не может вернуть ошибку напрямую,
например для колбэков библиотек. Команда, способная вернуть ошибку, должна
её возвращать.

## Тестирование модуля {#test-it}

`probe_test.go`:

```go
package probe_test

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"example.com/tt-probe"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/sdktest"
)

// cluster is the cluster configuration of the application "app": nothing
// listens on the address of its storage.
const cluster = `
groups:
  g:
    replicasets:
      r:
        instances:
          router: {}
          storage:
            iproto:
              listen:
              - uri: '127.0.0.1:1'
`

func TestInstances(t *testing.T) {
	services := sdktest.New(t, sdktest.WithClusterConfig(sdk.AppSource("app"), cluster))

	got := services.Run(probe.New, "probe", "instances", "app", "-o", "json")

	require.NoError(t, got.Err)
	assert.JSONEq(t, `{"app": "app", "instances": ["router", "storage"]}`, got.Stdout)
}

func TestInstancesOfInstance(t *testing.T) {
	got := sdktest.Run(t, probe.New, "probe", "instances", "app:router")

	var usage *sdk.UsageError
	require.ErrorAs(t, got.Err, &usage)
	assert.Equal(t, sdk.ExitFailure, got.ExitCode())
}

func TestInstancesNoApp(t *testing.T) {
	got := sdktest.Run(t, probe.New, "probe", "instances", "app")

	require.ErrorIs(t, got.Err, sdk.ErrNotFound)
}

// closedAddress returns a local TCP address nothing listens on.
func closedAddress(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	return address
}

func TestPingUnreachable(t *testing.T) {
	got := sdktest.Run(t, probe.New, "probe", "ping", closedAddress(t))

	require.Error(t, got.Err)
	assert.Equal(t, sdk.ExitSystem, got.ExitCode())
}

func TestPingApp(t *testing.T) {
	services := sdktest.New(t, sdktest.WithClusterConfig(sdk.AppSource("app"), cluster))

	got := services.Run(probe.New, "probe", "ping", "app:storage")

	require.ErrorContains(t, got.Err, "connecting to app:storage")
	assert.Equal(t, sdk.ExitSystem, got.ExitCode())
}

func TestPingCredentialsTwice(t *testing.T) {
	got := sdktest.Run(t, probe.New, "probe", "ping", "-u", "admin",
		"admin:secret@"+closedAddress(t))

	var usage *sdk.UsageError
	require.ErrorAs(t, got.Err, &usage)
}

func TestSnapshotDeclined(t *testing.T) {
	services := sdktest.New(t, sdktest.WithAnswers(false))

	got := services.Run(probe.New, "probe", "snapshot", closedAddress(t))

	require.Error(t, got.Err)
	assert.Contains(t, got.Stderr, "Make a snapshot on")
	assert.Equal(t, sdk.ExitFailure, got.ExitCode())
}

func TestSnapshotNoPrompt(t *testing.T) {
	services := sdktest.New(t, sdktest.WithNoPrompt())

	got := services.Run(probe.New, "probe", "snapshot", closedAddress(t))

	require.ErrorContains(t, got.Err, "cancelled")
	assert.Empty(t, got.Stderr) // No question asked.
}
```

`sdktest.Run` монтирует команды конструктора в корень, как это делает
ядро, и запускает командную строку; `sdktest.New` сначала принимает
параметры: `WithClusterConfig` задаёт источнику кластерную конфигурацию
(любой другой источник вернёт ошибку ненахождения), `WithAnswers` задаёт
ответы `Confirm`, `WithNoPrompt` заставляет `Confirm` возвращать fallback, а
`WithTarantool`, `WithProject`, `WithStdin` и `WithIntegrity` настраивают
остальные сервисы. `Result` содержит ошибку, stdout, stderr и записи лога;
`ExitCode` возвращает код, присвоенный ошибке SDK, без применения правил
ядра относительно сети и прав доступа. Принтеры sdktest сериализуют `json`,
но не `yaml` (его сериализует ядро). Точка монтирования, которую отвергло бы
ядро, приводит к провалу теста.

```console
$ go mod tidy
$ go test ./...
ok  	example.com/tt-probe	0.031s
```

## Встраивание в tt {#build-it-into-tt}

Рядом с модулем создаётся `dist/tt.manifest.toml`:

```toml
manifest_version = "0.1"
manifest_kind = "tt-distribution"

[core]
tt = "v3.0.0-alpha.2"

[dependencies]
probe = "../tt-probe"
```

```console
$ cd dist
$ tt binary build tt
$ ./tt version --modules
Tarantool CLI version 3.0.0-alpha.2, linux/amd64. commit: <unknown>
  builtin  v3.0.0-alpha.2  github.com/tarantool/tt/v3
  probe    (devel)         example.com/tt-probe
$ ./tt probe instances myapp -o yaml
app: myapp
instances:
  - a
  - b
$ ./tt probe ping 127.0.0.1:1
   ⨯ connecting to 127.0.0.1:1: failed to dial: dial tcp 127.0.0.1:1: connect: connection refused
$ echo $?
2
```

Имя в секции `[dependencies]` становится именем модуля в tt: в выводе `tt
version --modules`, в записях его логов и в ошибках, связанных с его
командами. Файлы `tt.manifest.lock` и `tt.manifest.sum`, созданные при
сборке, коммитят в репозиторий. После публикации модуля манифест ссылается
на него по пути к модулю и версии: `probe =
"example.com/tt-probe@v0.1.0"`. Все поля манифеста описаны в
[tt.manifest.toml](../reference/tt-manifest.md).

## Что отклоняет ядро {#what-the-core-refuses}

tt монтирует команды всех модулей до выполнения любой из них и отклоняет те,
которые его дерево команд не может принять, указывая имя модуля; после этого
бинарный файл завершается с ошибкой при любом запуске.

- Флаги: команда модуля не может объявлять флаги `-h`/`--help`,
  `-C`/`--directory`, флаги корня (`-I`/`--internal`, `-V`/`--verbose`,
  `--log-format`, `--no-prompt`, а в дистрибутиве с проверкой целостности
  также `--integrity-check` и `--integrity-check-period`), а также
  персистентный флаг группы, под которую она смонтирована, - ни по полному
  имени, ни по короткой форме. Короткая форма флага, использованная дважды
  внутри команд одного модуля, также отклоняется.
- Имена: команда, чьё имя или алиас уже заняты рядом с ней, отклоняется:
  дубликат - при совпадении имён, неоднозначность - при пересечении имени и
  алиаса. Точно так же отклоняются `help`, `__complete` и
  `__completeNoDesc` в корне. Команда в корне, названная так же, как одна из
  собственных команд tt, считается дубликатом: чтобы заменить команду tt,
  дистрибутив собирают с `builtin = false` либо используют внешний модуль.
- Пути: путь монтирования задаёт команды по их именам, а не по алиасам, и не
  может вести под исполняемую команду другого модуля. Группа, не являющаяся
  самостоятельной командой, считается общей: любой модуль может
  монтироваться под неё.

Как выполняются команды:

- Флаги корня указываются до команды: `tt -V probe ping ...`, а не `tt probe
  ping -V`, где `-V` считается неизвестным флагом. Команды модуля не видят
  флаги `-V` или `--no-prompt`; они видят их результаты через `Log` и
  `Confirm`.
- Неперсистентный флаг группы разбирается только между группой и
  подкомандой, `tt probe --flag ping`, поскольку корень tt обходит группы;
  корень sdktest этого не делает. Флаг, общий для подкоманд, должен быть
  персистентным флагом группы.
- Ядро оборачивает хуки `PersistentPreRunE`, `PreRunE`, `RunE`, `PostRunE` и
  `PersistentPostRunE`, выводя возвращаемые ими ошибки. Хуки без буквы `E`
  ничего не возвращают.
- `Services.Project().Dir()` возвращает рабочий каталог, в котором запущен
  tt. Модуль не может объявлять флаг `-C`; только команда, смонтированная
  под `tt package`, наследует его `-C` и получает этот каталог.
