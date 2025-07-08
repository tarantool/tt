import os
import subprocess

import utils


class Tt(object):
    def __init__(self, tt_cmd, app_path, instances):
        self.__tt_cmd = tt_cmd
        if os.path.isdir(app_path):
            self.__work_dir = app_path
        else:
            self.__work_dir = os.path.splitext(app_path)[0]
            os.mkdir(self.__work_dir)
        self.__instances = instances

    @property
    def instances(self):
        return self.__instances

    def instances_of(self, *targets):
        def is_instance_of(inst, *targets):
            app_name, sep, _ = inst.partition(":")
            for target in targets:
                if target is None or target == inst:
                    return True
                if sep == "":
                    continue
                target_app_name, target_sep, _ = target.partition(":")
                if target_sep == "" and target_app_name == app_name:
                    return True
            return False

        return [inst for inst in self.instances if is_instance_of(inst, *targets)]

    def exec(self, *args, **kwargs):
        args = list(filter(lambda x: x is not None, args))
        cmd = [self.__tt_cmd, *args]
        tt_kwargs = dict(cwd=self.__work_dir)
        tt_kwargs.update(kwargs)
        return utils.run_command_and_get_output(cmd, **tt_kwargs)

    def run(self, *args, **kwargs):
        """Works like subprocess.run (actually it is invoked), but:
        1. Command arguments are passed as separate function arguments and starts with the one
        that follows executable argument (None arguments are filtered out)
        2. Other default values are used for several keywords, namely:
            cwd=<APP_DIR>,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        3. 'env' is merged with the original environment instead of using it as standalone env.
        """
        args = list(filter(lambda x: x is not None, args))
        cmd = [self.__tt_cmd, *args]
        return subprocess.run(cmd, **self.__tt_kwargs(**kwargs))

    def popen(self, *args, **kwargs):
        """Works like subprocess.Popen with the same difference as in Tt.run."""
        args = list(filter(lambda x: x is not None, args))
        cmd = [self.__tt_cmd, *args]
        return subprocess.Popen(cmd, **self.__tt_kwargs(**kwargs))

    def path(self, *paths):
        return os.path.join(self.__work_dir, *paths)

    def inst_path(self, inst, *paths):
        app_name, sep, inst_name = inst.partition(":")
        if sep == "":
            inst_name = app_name
        return os.path.join(self.path(*paths), inst_name)

    def run_path(self, inst, *paths):
        return os.path.join(self.inst_path(inst, utils.run_path), *paths)

    def lib_path(self, inst, *paths):
        return os.path.join(self.inst_path(inst, utils.lib_path), *paths)

    def log_path(self, inst, *paths):
        return os.path.join(self.inst_path(inst, utils.log_path), *paths)

    def __tt_kwargs(self, **kwargs):
        tt_kwargs = dict(
            cwd=self.__work_dir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )
        tt_kwargs.update(kwargs)
        if "env" in kwargs:
            tt_env = os.environ.copy()
            tt_env.update(kwargs["env"])
            tt_kwargs["env"] = tt_env
        return tt_kwargs


def status(tt, *args):
    rc, out = tt.exec("status", *args)
    assert rc == 0
    return utils.extract_status(out)


def pid_files(tt, instances):
    return [tt.run_path(inst, utils.pid_file) for inst in instances]


def log_files(tt, instances):
    return [tt.log_path(inst, utils.log_file) for inst in instances]


def snap_files(tt, instances):
    return [tt.lib_path(inst, "*.snap") for inst in instances]


def wal_files(tt, instances):
    return [tt.lib_path(inst, "*.xlog") for inst in instances]


def wait_box_status(timeout, tt, instances, acceptable_statuses, interval=0.1):
    def are_all_box_statuses_acceptable(tt, instances, acceptable_statuses):
        status_ = status(tt)
        return all([(status_[inst].get("BOX") in acceptable_statuses) for inst in instances])

    return utils.wait_event(
        timeout,
        are_all_box_statuses_acceptable,
        interval,
        tt,
        instances,
        acceptable_statuses,
    )


def post_start_base(tt):
    assert utils.wait_files(5, pid_files(tt, tt.running_instances))


def post_start_cluster_decorator(func):
    def wrapper_func(tt):
        func(tt)
        # 'cluster' decoration.
        assert wait_box_status(5, tt, tt.running_instances, ["loading", "running"])

    return wrapper_func


def post_start_no_script_decorator(func):
    def wrapper_func(tt):
        func(tt)
        # 'no_script' decoration.
        flag_files = [tt.path(f"flag-{inst}") for inst in tt.running_instances]
        assert utils.wait_files(5, flag_files)
        os.remove(tt.path("init.lua"))

    return wrapper_func


def post_start_no_config_decorator(func):
    def wrapper_func(tt):
        func(tt)
        # 'no_config' decoration.
        flag_files = [tt.run_path(inst, "flag") for inst in tt.running_instances]
        assert utils.wait_files(5, flag_files)
        os.remove(tt.path("config.yaml"))

    return wrapper_func
