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

import asyncio
import re
import sys
import logging
import time
from os import getenv
from functools import wraps
from typing import Optional, Dict, Any, Tuple, List, Iterator, Callable, Awaitable
from lxml import etree
from podimo.client import PodimoClient, PodcastNotFoundError, PodimoError, AuthenticationError
from feedgen.feed import FeedGenerator
from mimetypes import guess_type
from aiohttp import ClientSession, CookieJar, ClientTimeout
from quart import Quart, Response, render_template, request, jsonify
from hashlib import sha256
from hypercorn.config import Config
from hypercorn.asyncio import serve
from urllib.parse import quote
from podimo.config import *
from podimo.utils import generateHeaders, randomHexId, video_exists_at_url
import podimo.cache as cache
import cloudscraper
import traceback

# Setup Quart, used for serving the web pages
app = Quart(__name__)
proxies: Dict[str, str] = dict()

#Setup logging
logging.basicConfig(
    format="%(levelname)s | %(asctime)s | %(message)s",
    datefmt="%Y-%m-%dT%H:%M:%SZ",
    level=logging.INFO,
)

proactive: Dict[str, List[float]] = dict()

def limit_request() -> Callable:
    def rate_limiter(func: Callable[..., Awaitable[Response]]) -> Callable[..., Awaitable[Response]]:
        @wraps(func)
        async def wrapper(*args: Any, **kwargs: Any) -> Response:
            ip = request.remote_addr or 'unknown'
            now = time.time()
            reqs: List[float] = proactive.get(ip, [])
            reqs = [t for t in reqs if now - t < 10]
            reqs.append(now)
            proactive[ip] = reqs
            if len(reqs) > 8:
                return Response('Rate limit exceeded', 429)
            return await func(*args, **kwargs)
        return wrapper

    return rate_limiter


@app.before_request
async def log_request_start() -> None:
    request._start_time = time.time()  # type: ignore[attr-defined]
    logging.debug(f"--> {request.method} {request.url} from {request.remote_addr} UA={request.user_agent}")

@app.after_request
async def log_request_end(response):
    start_time = getattr(request, '_start_time', None)
    if start_time:
        duration = time.time() - start_time
        logging.debug(f"<-- {request.method} {request.url} {response.status_code} ({duration:.3f}s)")
    else:
        logging.debug(f"<-- {request.method} {request.url} {response.status_code}")
    return response


def example() -> str:
    return f"""Example
------------
Username: example@example.com
Password: this-is-my-password
Podcast ID: 12345-abcdef

The URL will be
https://example%40example.com:this-is-my-password@{PODIMO_HOSTNAME}/feed/12345-abcdef.xml

Note that the username and password should be URL encoded. This can be done with
a tool like https://gchq.github.io/CyberChef/#recipe=URL_Encode(true)
"""

def authenticate() -> Response:
    return Response(
        f"""401 Unauthorized.
You need to login with the correct credentials for Podimo.

{example()}""",
        401,
        {
            "Content-Type": "text/plain",
            "WWW-Authenticate": "Basic realm='Podimo credentials'"
        },
    )

def initialize_client(username: str, password: str, region: str, locale: str) -> PodimoClient:
    client = PodimoClient(username, password, region, locale)

    # Check if there is an authentication token already in memory. If so, use that one.
    # If it is expired, request a new token.
    key = client.key
    client.token = cache.getCacheEntry(key, cache.TOKENS)

    # Check if we previously created a cookie jar
    if key not in cache.cookie_jars:
        cache.cookie_jars[key] = CookieJar()
    client.cookie_jar = cache.cookie_jars[key]
    return client

async def check_auth(username: str, password: str, region: str, locale: str, scraper: Any) -> Optional[PodimoClient]:
    try:
        client = initialize_client(username, password, region, locale)
        if client.token:
            return client

        await client.podimoLogin(scraper)
        if client.token:
            cache.insertIntoTokenCache(client.key, client.token)
        return client

    except Exception as e:
        logging.error(f"An error occurred: {e}")
        if DEBUG:
            traceback.print_exc()
    return None

podcast_id_pattern = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", re.IGNORECASE)

@app.route("/health")
async def health():
    """Health check endpoint for Docker orchestration.

    Returns a 200 OK with basic service info. Lightweight enough
    for frequent container health probes and load balancer checks.
    """
    return jsonify({"status": "ok", "service": "podimo-rss"})

@app.route("/search")
@limit_request()
async def search_podcasts():
    """Search for podcasts by name using Podimo's autocomplete endpoint."""
    search_query = request.args.get("q", "").strip()
    if not search_query or len(search_query) < 2:
        return Response("Query must be at least 2 characters", 400)

    if LOCAL_CREDENTIALS:
        username = PODIMO_EMAIL
        password = PODIMO_PASSWORD
        region = request.args.get("region", "nl")
        locale = request.args.get("locale", "nl-NL")
    else:
        auth = request.authorization
        if not auth:
            return authenticate()
        username, region, locale = split_username_region_locale(auth.username)
        password = auth.password

    if region not in [rc for (rc, _) in REGIONS]:
        return Response("Invalid region", 400)
    if locale not in LOCALES:
        return Response("Invalid locale", 400)

    with cloudscraper.create_scraper() as scraper:
        scraper.proxies = proxies
        client = await check_auth(username, password, region, locale, scraper)
        if not client:
            return authenticate()
        try:
            results = await client.searchPodcasts(search_query, scraper)
            return jsonify({"results": results, "query": search_query})
        except PodimoError as e:
            logging.error(f"Podimo search error: {e}")
            return Response("Search failed — Podimo API error. Try searching from open.podimo.com directly.", 500)
        except Exception as e:
            logging.error(f"Unexpected search error: {e}")
            return Response("Search failed. Podimo may have changed their API.", 500)


@app.route("/subscriptions")
@limit_request()
async def subscriptions():
    """List podcasts the authenticated user follows."""
    if LOCAL_CREDENTIALS:
        username = PODIMO_EMAIL
        password = PODIMO_PASSWORD
        region = request.args.get("region", "nl")
        locale = request.args.get("locale", "nl-NL")
    else:
        auth = request.authorization
        if not auth:
            return authenticate()
        username, region, locale = split_username_region_locale(auth.username)
        password = auth.password

    if region not in [rc for (rc, _) in REGIONS]:
        return Response("Invalid region", 400)
    if locale not in LOCALES:
        return Response("Invalid locale", 400)

    with cloudscraper.create_scraper() as scraper:
        scraper.proxies = proxies
        client = await check_auth(username, password, region, locale, scraper)
        if not client:
            return authenticate()
        try:
            results = await client.getFollowedPodcasts(scraper)
            return jsonify({"results": results})
        except PodimoError as e:
            logging.error(f"Podimo subscriptions error: {e}")
            return Response("Failed to fetch subscriptions", 500)


@app.route("/", methods=["POST", "GET"])
async def index():
    error = ""
    if request.method == "POST":
        form = await request.form
        email = form.get("email")
        password = form.get("password")
        podcast_id = form.get("podcast_id")
        region = form.get("region")
        locale = form.get("locale")

        if not LOCAL_CREDENTIALS:
            if email is None or email == "":
                error += "Email is required"
            if password is None or password == "":
                error += "Password is required"
        if podcast_id is None or podcast_id == "":
            error += "Podcast ID is required"
        elif podcast_id_pattern.fullmatch(podcast_id) is None:
            error += "Podcast ID is not valid"
        if region is None or region == "":
            error += "Region is required"
        elif region not in [region_code for (region_code, _) in REGIONS]:
            error += "Region is not valid"
        if locale is None or locale == "":
            error += "Locale is required"
        elif locale not in LOCALES:
            error += "Locale is not valid"

        if error == "":
            podcast_id = quote(str(podcast_id), safe="")
            region = quote(str(region), safe="")
            locale = quote(str(locale), safe="")
            
            if LOCAL_CREDENTIALS:
                url = f"{PODIMO_PROTOCOL}://{PODIMO_HOSTNAME}/feed/{podcast_id}.xml?{randomHexId(10)}&region={region}&locale={locale}"
            else:
                email = quote(str(email), safe="")
                comma = quote(',', safe="")
                username = f"{email}{comma}{region}{comma}{locale}"
                password = quote(str(password), safe="")             
                url = f"{PODIMO_PROTOCOL}://{username}:{password}@{PODIMO_HOSTNAME}/feed/{podcast_id}.xml?{randomHexId(10)}&region={region}&locale={locale}"
            
            logging.debug(f"Created an URL: {url}.")
            return await render_template("feed_location.html", url=url)

    return await render_template("index.html", error=error, locales=LOCALES, regions=REGIONS, need_credentials=not(LOCAL_CREDENTIALS))


@app.errorhandler(404)
async def not_found(error):
    return Response(
        f"404 Not found.\n\n{example()}", 404, {"Content-Type": "text/plain"}
    )


@app.route("/feed/<string:podcast_id>.xml")
@limit_request()
async def serve_basic_auth_feed(podcast_id):
    if LOCAL_CREDENTIALS:
        args = request.args
        region = args.get("region")
        locale = args.get("locale")
        return await serve_feed(PODIMO_EMAIL, PODIMO_PASSWORD, podcast_id, region, locale)
    else:
        auth = request.authorization
        if not auth:
            return authenticate()
        else:
            username, region, locale = split_username_region_locale(auth.username)
            return await serve_feed(username, auth.password, podcast_id, region, locale)


def split_username_region_locale(string: str) -> Tuple[str, str, str]:
    s = string.split(',')
    if len(s) == 3:
        return (s[0], s[1], s[2])
    else:
        return (s[0], 'nl', 'nl-NL')


def token_key(username: str, password: str) -> str:
    key = sha256(
        b"~".join([username.encode("utf-8"), password.encode("utf-8")])
    ).hexdigest()
    return key


@app.route("/feed/<string:username>/<string:password>/<string:podcast_id>.xml")
@limit_request()
async def serve_feed(username: str, password: str, podcast_id: str, region: str, locale: str) -> Response:
    
    logging.debug(f"Feed request for podcast {podcast_id} from IP {request.remote_addr} with User-Agent:{request.user_agent}.")
    
    # Check if it is a valid podcast id string
    if podcast_id_pattern.fullmatch(podcast_id) is None:
        return Response("Invalid podcast id format", 400, {})
   
    if region not in [region_code for (region_code, _) in REGIONS]:
        return Response("Invalid region", 400, {})
    if locale not in LOCALES:
        return Response("Invalid locale", 400, {})

    # Check if podcastID in blocked list. If so, return HTTP code 410 GONE
    if podcast_id in BLOCKED:
        logging.debug(f"Blocked! Podcast {podcast_id} is on local block list")
        return Response("Podcast is gone", 410, {}) 
    
    with cloudscraper.create_scraper() as scraper:
        scraper.proxies = proxies
        client = await check_auth(username, password, region, locale, scraper)
        if not client:
            return authenticate()

        # Get a list of valid podcasts
        try:
            podcasts = await podcastsToRss(
                podcast_id, await client.getPodcasts(podcast_id, scraper), locale
            )
        except PodimoError as e:
            if isinstance(e, PodcastNotFoundError):
                return Response(
                    "Podcast not found. Are you sure you have the correct ID?", 404, {}
                )
            logging.error(f"Podimo API error: {e}")
            return Response("Something went wrong while fetching the podcasts", 500, {})
        except Exception as e:
            # Catch-all for unexpected errors (network, parsing, etc.)
            logging.error(f"Unexpected error while fetching podcasts: {e}")
            return Response("Something went wrong while fetching the podcasts", 500, {})
        return Response(podcasts, mimetype="text/xml")


async def urlHeadInfo(session: ClientSession, id: str, url: str, locale: str) -> Tuple[str, str]:
    entry = cache.getHeadEntry(id)
    if entry:
        return entry

    retries = 3  # Number of retries
    timeout = ClientTimeout(total=10)  # 10 seconds timeout for each try

    for attempt in range(retries):
        try:
            logging.debug(f"HEAD request to {url} (Attempt {attempt + 1})")
            async with session.head(url, allow_redirects=True,
                                    headers=generateHeaders(None, locale),
                                    timeout=timeout) as response:
                content_length = '0'
                content_type, _ = guess_type(url)
                if 'content-length' in response.headers:
                    content_length = response.headers['content-length']
                if content_type is None:
                    content_type = response.headers.get('content-type', 'audio/mpeg')
                cache.insertIntoHeadCache(id, content_length, content_type)
                return (content_length, content_type)

        except asyncio.TimeoutError:
            if attempt < retries - 1:
                logging.info(f"Retrying HEAD request to {url} (Attempt {attempt + 2})")
                await asyncio.sleep(1)  # Wait for 1 second before retrying
            else:
                logging.error(f"All retries failed for HEAD request to {url}")
                raise  # Re-raise the last exception if all retries fail



    # Unreachable, but satisfies mypy's control flow analysis
    return ('0', 'audio/mpeg')


def extract_audio_url(episode: Dict[str, Any]) -> Tuple[Optional[str], int]:
    duration = 0
    url = None
    if episode['audio']:
        url = episode['audio']['url']
        duration = episode['audio']['duration']

    if url is None or url == "":
        if episode["streamMedia"]:
            url = episode["streamMedia"]["url"]
            duration = episode["streamMedia"]["duration"]
            if "hls-media" in url and "/main.m3u8" in url:
                url = url.replace("hls-media", "audios")
                url = url.replace("/main.m3u8", ".mp3")

    return url, duration


async def addFeedEntry(fg: FeedGenerator, episode: Dict[str, Any], session: ClientSession, locale: str) -> None:
    url, duration = extract_audio_url(episode)
    if url is None:
        return

    fe = fg.add_entry()
    fe.guid(episode["id"])

    # Generate the video url and paste it as prefix in the description :')
    ep_id = url.split("/")[-1].replace(".mp3", "")
    hls_url = f"https://cdn.podimo.com/hls-media/{ep_id}/stream_video_high/stream.m3u8"

    if VIDEO_ENABLED:
        if VIDEO_CHECK_ENABLED:
            if await video_exists_at_url(hls_url):
                fe.description(
                    f"Video URL found at: {hls_url} (experimental) || {episode['description']}"
                )
                fe.title(episode["title"] + VIDEO_TITLE_SUFFIX)
            else:
                fe.description(f"Video URL: {hls_url} (not verified) || {episode['description']}")
                fe.title(episode["title"])
        else:
            fe.description(f"Video URL: {hls_url} (not verified) || {episode['description']}")
            fe.title(episode["title"])
    else:
        fe.description(episode["description"])
        fe.title(episode["title"])

    fe.pubDate(episode.get("publishDatetime", episode.get("datetime")))
    # feedgen's itunes_image() rejects .webp/extensionless URLs even though
    # podcast clients handle them fine. Bypass: write the element directly.
    image_url = episode.get("imageUrl")
    if image_url:
        try:
            fe.podcast.itunes_image(image_url)
        except ValueError:
            # Podimo serves .webp or extensionless CDN URLs that feedgen rejects.
            # Manually inject the element so podcast clients still get artwork.
            itunes_ns = "http://www.itunes.com/dtds/podcast-1.0.dtd"
            img = etree.SubElement(fe.lxml(), "{%s}image" % itunes_ns)
            img.set("href", str(image_url))
            logging.debug(f"Bypassed itunes_image validation for {episode['id']}")

    logging.debug(f"Found podcast '{episode['title']}'")
    fe.podcast.itunes_duration(duration)
    content_length, content_type = await urlHeadInfo(session, episode['id'], url, locale)
    fe.enclosure(url, content_length, content_type)

def chunks(x: List[Any], n: int) -> Iterator[List[Any]]:
    for i in range(0, len(x), n):
        yield x[i:i + n]

async def podcastsToRss(podcast_id: str, data: Dict[str, Any], locale: str) -> bytes:
    fg = FeedGenerator()
    fg.load_extension("podcast")

    podcast = data["podcast"]
    episodes = data["episodes"]

    title = podcast.get("title")
    if title is None and len(episodes) > 0:
        title = episodes[0].get("podcastName", "Unknown")
    if title is None:
        title = "Unknown"
    fg.title(title)

    if podcast.get("description"):
        fg.description(podcast["description"])
    else:
        fg.description(title)

    fg.link(href=f"https://podimo.com/shows/{podcast_id}", rel="alternate")

    images = podcast.get("images", {})
    image = images.get("coverImageUrl") if images else None
    if image is None and len(episodes) > 0:
        image = episodes[0].get("imageUrl")
    try:
        fg.image(image)
    except ValueError:
        # feedgen rejects .webp/extensionless URLs. Bypass for channel image.
        if image:
            itunes_ns = "http://www.itunes.com/dtds/podcast-1.0.dtd"
            img = etree.SubElement(fg.lxml().find("channel"), "{%s}image" % itunes_ns)
            img.set("href", str(image))
            logging.debug(f"Bypassed channel image validation for podcast")

    language = podcast.get("language")
    if language is None:
        language = locale
    fg.language(language)

    artist = podcast.get("authorName")
    if artist is None and len(episodes) > 0:
        artist = episodes[0].get("artist")
    fg.podcast.itunes_author(artist)

    if not PUBLIC_FEEDS:
        fg.podcast.itunes_block(True)

    async with ClientSession() as session:
        for chunk in chunks(episodes, 5):
            await asyncio.gather(
                *[addFeedEntry(fg, episode, session, locale) for episode in chunk]
            )

    feed: bytes = fg.rss_str(pretty=True)
    return feed


async def spawn_web_server():
    config = Config()
    config.bind = [PODIMO_BIND_HOST]
    config.read_timeout = 60
    config.graceful_timeout = 5
    config.backlog = 1000
    app.config['TEMPLATES_AUTO_RELOAD'] = True
    await serve(app, config)

async def main():
    if HTTP_PROXY:
        global proxies
        logging.info(f"Running with https proxy defined in environmental variable HTTP_PROXY: {HTTP_PROXY}")
        proxies['https'] = HTTP_PROXY
    tasks = [spawn_web_server()]
    await asyncio.gather(*tasks)

if __name__ == "__main__":
    if DEBUG:
        logging.info(f"""Spawning server on {PODIMO_BIND_HOST}
Configuration: 
- DEBUG: {DEBUG}
- LOCAL CREDENTIALS: {LOCAL_CREDENTIALS} ({PODIMO_EMAIL})
- PODIMO_HOSTNAME: {PODIMO_HOSTNAME}
- PODIMO_BIND_HOST: {PODIMO_BIND_HOST}
- PODIMO_PROTOCOL: {PODIMO_PROTOCOL}
- PUBLIC_FEEDS: {PUBLIC_FEEDS}
- HTTP_PROXY: {HTTP_PROXY}
- ZENROWS_API: {ZENROWS_API}
- SCRAPER_API: {SCRAPER_API}
- CACHE_DIR: {CACHE_DIR}
- STORE_TOKENS_ON_DISK: {STORE_TOKENS_ON_DISK}
- TOKEN_CACHE_TIME: {TOKEN_CACHE_TIME} sec
- PODCAST_CACHE_TIME: {PODCAST_CACHE_TIME} sec
- HEAD_CACHE_TIME: {HEAD_CACHE_TIME} sec
- BLOCKING: {BLOCKED}
""")
    asyncio.run(main())
