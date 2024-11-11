import etcd3
import yaml
from copy import deepcopy

class ClusterConfig:
    def __init__(self, host: str, port: int, cfg_key='/tdb/config/all'):
        self.etcd = etcd3.client(host=host, port=port)
        self.cfg_key = cfg_key
        self._config = yaml.safe_load(self.etcd.get(self.cfg_key)[0])

    @property
    def Config(self) -> dict:
        return deepcopy(self._config)

    @Config.setter
    def Config(self, cfg: dict):
        self._config = cfg
        self.etcd.put(self.cfg_key, yaml.safe_dump(cfg))
