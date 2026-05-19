# Copyright 2022 Thijs Raymakers
#
# Licensed under the EUPL, Version 1.2 or – as soon they
# will be approved by the European Commission - subsequent
# versions of the EUPL (the "Licence");
# You may not use this work except in compliance with the
# Licence.
# You may obtain a copy of the Licence at:
#
# https://joinup.ec.europa.eu/software/page/eupl
#
# Unless required by applicable law or agreed to in
# writing, software distributed under the Licence is
# distributed on an "AS IS" basis,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
# express or implied.
# See the Licence for the specific language governing
# permissions and limitations under the Licence.

from email.utils import parseaddr
from random import choice, randint
from hashlib import sha256
import asyncio
from functools import wraps, partial
from typing import Optional, Dict, Any, Callable

def randomHexId(length: int) -> str:
    string = []
    hex_chars = list("1234567890abcdef")
    for _ in range(length):
        string.append(choice(hex_chars))
    return "".join(string)


def randomFlyerId() -> str:
    a = randint(1000000000000, 9999999999999)
    b = randint(1000000000000, 9999999999999)
    return str(f"{a}-{b}")


def token_key(username: str, password: str) -> str:
    key = sha256(
        b"~".join([username.encode("utf-8"), password.encode("utf-8")])
    ).hexdigest()
    return key

# Verify if it is actually an email address
def is_correct_email_address(username: str) -> bool:
    return "@" in parseaddr(username)[1]


def generateHeaders(authorization: Optional[str], locale: str) -> Dict[str, str]:
    headers = {
        'user-os': 'android',
        'user-agent': 'Podimo/2.45.1 build 566/Android 33',
        'user-version': '2.45.1',
        'user-locale': locale,
        "user-unique-id": randomHexId(16)
    }
    if authorization:
        headers["authorization"] = authorization
    return headers


def async_wrap(func: Callable) -> Callable:
    @wraps(func)
    async def run(*args: Any, loop: Optional[asyncio.AbstractEventLoop] = None, executor: Optional[Any] = None, **kwargs: Any):
        if loop is None:
            loop = asyncio.get_event_loop()
        pfunc = partial(func, *args, **kwargs)
        return await loop.run_in_executor(executor, pfunc)
    return run

async def video_exists_at_url(url: str) -> bool:
    from aiohttp import ClientSession, ClientTimeout
    try:
        async with ClientSession() as session:
            async with session.get(url, timeout=ClientTimeout(total=10)) as res:
                text = await res.text()
                return text.strip().startswith("#EXTM3U")
    except Exception:
        return False
