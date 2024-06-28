import os
import subprocess

import etcd3

etcd_username = "root"
etcd_password = "password"


class EtcdInstance():
    def __init__(self, host, port, workdir):
        self.host = host
        self.port = port
        self.workdir = workdir
        self.endpoint = f"http://{self.host}:{self.port}"
        self.popen = None

    def start(self):
        popen = subprocess.Popen(
            [os.getenv("ETCD_PATH", default="") + "etcd"],
            env={"ETCD_LISTEN_CLIENT_URLS": self.endpoint,
                 "ETCD_ADVERTISE_CLIENT_URLS": self.endpoint,
                 "PATH": os.getenv("PATH")},
            cwd=self.workdir,
            stderr=subprocess.STDOUT,
            stdout=subprocess.PIPE,
            text=True,
        )

        try:
            popen.wait(1)
        except Exception:
            pass

        self.popen = popen if not popen.poll() else None
        assert self.popen

    def stop(self):
        if self.popen:
            self.popen.kill()
            self.popen.wait()
            self.popen = None

    def conn(self) -> etcd3.Etcd3Client:
        etcd = etcd3.client(self.host, self.port)
        return etcd

    def truncate(self):
        try:
            subprocess.run(["etcdctl", "del", "--prefix", "/", f"--endpoints={self.endpoint}"])
        except Exception as ex:
            self.stop()
            raise ex

    def enable_auth(self):
        # etcdv3 client have a bug that prevents to establish a connection with
        # authentication enabled in latest python versions. So we need a separate steps
        # to upload/fetch data to/from etcd via the client.
        try:
            subprocess.run(["etcdctl", "user", "add", etcd_username,
                           f"--new-user-password={etcd_password}",
                            f"--endpoints={self.endpoint}"])
            subprocess.run(["etcdctl", "auth", "enable",
                           f"--user={etcd_username}:{etcd_password}",
                            f"--endpoints={self.endpoint}"])
        except Exception as ex:
            self.stop()
            raise ex

    def disable_auth(self):
        subprocess.run(["etcdctl", "auth", "disable",
                        f"--user={etcd_username}:{etcd_password}",
                        f"--endpoints={self.endpoint}"])
