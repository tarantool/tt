import twr_python_sdk
twr_python_sdk.install(ports=['ssl', 'asyncio_dns'])

import wit_world


class WitWorld(wit_world.WitWorld):
    def add(self, a: int, b: int) -> int:
        return a + b
