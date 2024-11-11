import pytest

from config import ClusterConfig
from tdb_client import TDBClient
import consts

@pytest.fixture(scope="module")
def tdb_client() -> TDBClient:
    return TDBClient(
        addrs=consts.TDB_ROUTERS,
        password=consts.TDB_PASSWORD,
        user=consts.TDB_USER,
    )

@pytest.fixture(scope="module")
def cluster_config() -> ClusterConfig:
    return ClusterConfig(
        host=consts.ETCD_HOST,
        port=consts.ETCD_PORT,
        cfg_key='/tdb/config/all',
    )
