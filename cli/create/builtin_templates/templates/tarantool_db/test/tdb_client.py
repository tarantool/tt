from tarantool import ConnectionPool, response, Mode
from typing import TypedDict, Tuple, Optional

class DictSetDelOptions(TypedDict):
    wait_consistency: bool
    wait_consistency_timeout: int

class DictGetKeysOptions(TypedDict):
    limit: int
    __annotations__['yield'] = int

class DictGetEntityOptions(TypedDict):
    limit: int
    __annotations__['yield'] = int


class TDBClient(ConnectionPool):
    def __init__(self, addrs:list,  user: str, password: str):
        super().__init__(
            addrs=addrs,
            user=user,
            password=password,
        )

    # https://github.com/tarantool/dictionary
    def dictionary_get(self, entity_name: str, key: str) -> response.Response:
        return super().call('dictionary_router_get', [entity_name, key], mode=Mode.ANY)
    
    def dictionary_set(self, entity_name: str, key: str, value: any, opts: DictSetDelOptions = {}) -> response.Response:
        return super().call('dictionary_router_set', [entity_name, key, value, opts], mode=Mode.ANY)

    def dictionary_del(self, entity_name: str, key: str, opts: DictSetDelOptions = {}) -> response.Response:
        return super().call('dictionary_router_del', [entity_name, key, opts], mode=Mode.ANY)

    def dictionary_get_keys(self, entity_name: str,opts: DictGetKeysOptions = {}) -> response.Response:
        return super().call('dictionary_router_get_keys', [entity_name, opts], mode=Mode.ANY)
    
    def dictionary_get_entity(self, entity_name: str,opts: DictGetEntityOptions = {}) -> response.Response:
        return super().call('dictionary_router_get_entity', [entity_name, opts], mode=Mode.ANY)

    def dictionary_get_batch(self, entity_name: str, key_list: list) -> response.Response:
        return super().call('dictionary_router_get_batch', [entity_name, key_list], mode=Mode.ANY)
    
    def dictionary_set_batch(self, entity_name: str, key_value: dict) -> response.Response:
        return super().call('dictionary_router_set_batch', [entity_name, key_value], mode=Mode.ANY)

    def dictionary_del_entity(self, entity_name: str) -> response.Response:
        return super().call('dictionary_router_del_entity', [entity_name], mode=Mode.ANY)

    def dictionary_check_consistency(self) -> response.Response:
        return super().call(func_name='dictionary_router_check_consistency', mode=Mode.ANY)

    def dictionary_check_key_consistency(self, entity_name:str,  key: str) -> response.Response:
        return super().call('dictionary_router_check_key_consistency', [entity_name, key], mode=Mode.ANY)

    def dictionary_wait_key_consistency(self, entity_name: str, key: str) -> response.Response:
        return super().call('dictionary_router_wait_key_consistency', [entity_name, key], mode=Mode.ANY)
    
    def dictionary_notify_neighbors(self) -> response.Response:
        return super().call('dictionary_router_notify_neighbors', mode=Mode.ANY)
    
    def dictionary_try_sync(self) -> response.Response:
        return super().call('dictionary_router_try_sync', mode=Mode.ANY)
