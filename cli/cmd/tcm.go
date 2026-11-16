package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/sdk/scope"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/tcm"
	"github.com/tarantool/tt/v3/cli/util"
	"github.com/tarantool/tt/v3/internal/binary"
)

// tcmInitOpts are the flags of tt tcm init.
var tcmInitOpts struct {
	version  string
	http     string
	template string
}

// errTCMTemplate is wrapped by the error for a --template the project does
// not have.
var errTCMTemplate = errors.New("no such template")

// tcmTemplateDirPerm is the mode of a template directory tt tcm init
// creates.
const tcmTemplateDirPerm os.FileMode = 0o755

// NewTcmCmd creates the tt tcm command group: tt tcm init. TCM runs as an
// instance of its application, under tt instance.
func NewTcmCmd() *cobra.Command {
	tcmCmd := &cobra.Command{
		Use:   "tcm",
		Short: "Declare TCM, the Tarantool Cluster Manager, in a project",
		Long: "TCM, the Tarantool Cluster Manager, is the web interface of the clusters " +
			"of Tarantool Enterprise. Each application has a TCM of its own, a record with " +
			"kind: tcm in its instances.yaml, which runs as any instance does: " +
			"tt instance start, stop, restart, status and logs, and tt service systemd. " +
			"tt tcm init declares it.",
	}

	tcmCmd.AddCommand(newTcmInitCmd())

	return tcmCmd
}

func newTcmInitCmd() *cobra.Command {
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Declare TCM in the instances.yaml of a template and in the manifest",
		Long: "Add the record of TCM, kind: tcm, to .templates/<template>/instances.yaml " +
			"of the project, and require a version of TCM in [platform].tcm of " +
			"app.manifest.toml, which tt package pack bundles into the package.\n\n" +
			"The version is --version: a version, which becomes the lowest one allowed, " +
			"such as >=1.3, or a constraint. Without it [platform].tcm is kept when it is " +
			"set, and set from the newest TCM tt binary installed otherwise; with none " +
			"installed the command fails, as only the customer zone of tarantool.io lists " +
			"the versions of TCM.\n\n" +
			"A project that has TCM already is left as it is, and the record is reported. " +
			"A project of Tarantool Community Edition is refused: TCM is Tarantool " +
			"Enterprise only. In a project without app.manifest.toml only the record is " +
			"added.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return commandError(cmd, runTcmInit())
		},
		Example: `# Declare TCM in the default template, at its default address.
  $ tt tcm init --version 1.3

  # Another address, in the template prod.
  $ tt tcm init --http 0.0.0.0:8081 --template prod`,
	}

	initCmd.Flags().StringVar(&tcmInitOpts.version, "version", "",
		"the version of TCM, or a constraint, for [platform].tcm")
	initCmd.Flags().StringVar(&tcmInitOpts.http, "http", "",
		"the address TCM listens on, host:port (default "+tcm.DefaultHTTP+")")
	initCmd.Flags().StringVar(&tcmInitOpts.template, "template", app.DefaultTemplate,
		"the template whose instances.yaml declares TCM")
	addProjectDirFlag(initCmd)

	return initCmd
}

// runTcmInit is tt tcm init: everything is checked and computed before
// anything is written, so that a refusal leaves the project as it was.
func runTcmInit() error {
	dir, err := projectRootDir()
	if err != nil {
		return err
	}

	man, manifestSrc, err := readInitManifest(dir)
	if err != nil {
		return err
	}

	name := filepath.Base(dir)
	if man != nil {
		name = man.Package.Name

		err = manifest.CheckTCM(man.Platform)
		if err != nil {
			return sdk.Errorf(sdk.ExitFailure, "%w", err)
		}
	}

	templateDir, err := initTemplateDir(dir, tcmInitOpts.template)
	if err != nil {
		return err
	}

	descFile := filepath.Join(templateDir, scope.InstancesFileName)

	desc, declared, err := initDescription(dir, templateDir, descFile)
	if err != nil {
		return err
	}

	edited, constraint, err := initManifest(man, manifestSrc)
	if err != nil {
		return err
	}

	err = writeInitFiles(dir, templateDir, descFile, desc, declared, edited)
	if err != nil {
		return err
	}

	reportTcmInit(dir, name, descFile, declared, constraint, edited != nil)

	return nil
}

// readInitManifest reads app.manifest.toml of the project in dir: nil for a
// project without one, which is warned about, and an error outside of any
// project.
func readInitManifest(dir string) (*manifest.Manifest, []byte, error) {
	path := filepath.Join(dir, manifestFileName)

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		_, found, findErr := scope.FindProject(dir)
		if findErr != nil || !found {
			return nil, nil, sdk.Errorf(sdk.ExitFailure, "%w: no %s, %s or %s found from %s up",
				errNotInProject, manifestFileName, scope.TemplatesDirName, scope.ProjectDirName,
				dir)
		}

		log.Warnf("There is no %s in %s: [platform].tcm is not set, and nothing says the "+
			"project runs on Tarantool Enterprise, which TCM manages clusters of; install "+
			"TCM with tt binary install tcm <version>.", manifestFileName, dir)

		return nil, nil, nil
	}

	if err != nil {
		return nil, nil, fmt.Errorf("cannot read %s: %w", path, err)
	}

	man, _, err := manifest.ParseManifest(data)
	if err != nil {
		return nil, nil, sdk.Errorf(sdk.ExitFailure, "%s: %w", path, err)
	}

	return man, data, nil
}

// initTemplateDir returns the directory of template in the project in dir.
// The default template is created when it is missing; another one has to be
// there, since a typo must not make a template of its own.
func initTemplateDir(dir, template string) (string, error) {
	if template == "" || strings.ContainsAny(template, `/\`) || strings.HasPrefix(template, ".") {
		return "", util.NewArgError(fmt.Sprintf("--template %q is not a template name",
			template))
	}

	templateDir := filepath.Join(dir, scope.TemplatesDirName, template)

	info, err := os.Stat(templateDir)

	switch {
	case err == nil && info.IsDir():
		return templateDir, nil
	case err == nil:
		return "", sdk.Errorf(sdk.ExitFailure, "%s is not a directory", templateDir)
	case !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("cannot look for the template: %w", err)
	case template == app.DefaultTemplate:
		return templateDir, nil
	default:
		return "", sdk.Errorf(sdk.ExitFailure, "%w %q in %s", errTCMTemplate, template,
			filepath.Join(dir, scope.TemplatesDirName))
	}
}

// initDescription returns the instances.yaml of the template with the record
// of TCM (tcm.AddRecord).
func initDescription(dir, templateDir, descFile string) ([]byte, tcm.Declared, error) {
	var declared tcm.Declared

	src, err := os.ReadFile(descFile)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		src = nil
	case err != nil:
		return nil, declared, fmt.Errorf("cannot read %s: %w", descFile, err)
	}

	_, configErr := os.Stat(filepath.Join(templateDir, scope.ConfigFileName))

	desc, declared, err := tcm.AddRecord(src, dir, configErr == nil, tcmInitOpts.http)
	if err != nil {
		return nil, declared, sdk.Errorf(sdk.ExitFailure, "%s: %w", descFile, err)
	}

	return desc, declared, nil
}

// initManifest returns the manifest src with [platform].tcm set, and the
// constraint it is set to; nil when it is left as it is, as without a
// manifest, or without --version when it is set. Without --version and
// [platform].tcm, the newest TCM installed in the scopes gives the version.
func initManifest(man *manifest.Manifest, src []byte) ([]byte, string, error) {
	if man == nil {
		return nil, "", nil
	}

	var constraint string

	switch {
	case tcmInitOpts.version != "":
		constraint = tcm.Constraint(tcmInitOpts.version)
	case man.Platform.Tcm.Version != "":
		return nil, man.Platform.Tcm.Version, nil
	default:
		newest, found := tcm.NewestInstalled(tcmScopeRoots())
		if !found {
			return nil, "", sdk.Errorf(sdk.ExitFailure, "%w: [platform].tcm of %s is not set, "+
				"and no TCM is installed to take its version from; pass --version, such as "+
				"--version 1.3: tt binary search tcm lists the versions with the credentials "+
				"of the customer zone of tarantool.io, which tt cannot ask without them",
				tcm.ErrNoVersion, manifestFileName)
		}

		constraint = tcm.Constraint(newest)
	}

	if constraint == man.Platform.Tcm.Version {
		return nil, constraint, nil
	}

	editor, err := manifest.NewEditor(src)
	if err != nil {
		return nil, "", sdk.Errorf(sdk.ExitFailure, "%s: %w", manifestFileName, err)
	}

	_, err = editor.SetPlatformTCM(constraint)
	if err != nil {
		return nil, "", sdk.Errorf(sdk.ExitFailure, "%s: %w", manifestFileName, err)
	}

	edited := editor.Bytes()

	_, _, err = manifest.ParseManifest(edited)
	if err != nil {
		return nil, "", util.NewArgError(fmt.Sprintf("--version %q: %s", tcmInitOpts.version,
			err))
	}

	return edited, constraint, nil
}

// tcmScopeRoots are the roots tt binary installs TCM into that can be found:
// the project, the user and the system scopes.
func tcmScopeRoots() []string {
	var roots []string

	project, err := projectRootDir()
	if err == nil {
		roots = append(roots, scope.ProjectRoot(project))
	}

	for _, root := range []func() (string, error){scope.UserRoot, scope.SystemRoot} {
		path, err := root()
		if err == nil {
			roots = append(roots, path)
		}
	}

	return roots
}

// writeInitFiles writes the description of the template and the manifest
// edited, those that changed.
func writeInitFiles(dir, templateDir, descFile string, desc []byte, declared tcm.Declared,
	edited []byte,
) error {
	if declared.Existing == "" {
		err := os.MkdirAll(templateDir, tcmTemplateDirPerm)
		if err != nil {
			return fmt.Errorf("cannot create the template: %w", err)
		}

		err = os.WriteFile(descFile, desc, manifestPerm)
		if err != nil {
			return fmt.Errorf("cannot write %s: %w", descFile, err)
		}
	}

	if edited != nil {
		path := filepath.Join(dir, manifestFileName)

		err := os.WriteFile(path, edited, manifestPerm)
		if err != nil {
			return fmt.Errorf("cannot write %s: %w", path, err)
		}
	}

	return nil
}

// reportTcmInit says what tt tcm init did, and what to do next.
func reportTcmInit(dir, name, descFile string, declared tcm.Declared, constraint string,
	setConstraint bool,
) {
	rel := func(path string) string {
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return path
		}

		return relative
	}

	record := tcm.RecordName

	if declared.Existing != "" {
		record = declared.Existing
		log.Infof("%s declares TCM already, as %q: it is left as it is.", rel(descFile),
			declared.Existing)

		if tcmInitOpts.http != "" && tcmInitOpts.http != declared.HTTP {
			log.Warnf("--http %s is not applied: edit the http field of %q in %s.",
				tcmInitOpts.http, declared.Existing, rel(descFile))
		}
	} else {
		log.Infof("Declared TCM as %q in %s.", tcm.RecordName, rel(descFile))
	}

	switch {
	case setConstraint:
		log.Infof("Set [platform].tcm = %q in %s.", constraint, manifestFileName)
	case constraint != "":
		log.Infof("[platform].tcm of %s is %q.", manifestFileName, constraint)
	}

	target := name + ":" + record

	var steps []string

	active := filepath.Join(scope.ProjectRoot(dir), binary.BinDirName, binary.ProgramTcm.Exec())

	_, err := os.Stat(active)
	if err != nil {
		steps = append(steps, "tt binary install tcm <version>   # Tarantool Enterprise: "+
			"--credentials-file with the credentials of the customer zone")
	}

	steps = append(steps,
		"tt instance start "+target,
		"tt instance status "+target+" --details   # the URL, the user and the password file")

	log.Infof("Next:\n    %s", strings.Join(steps, "\n    "))
}
