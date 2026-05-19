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

from podimo.config import GRAPHQL_URL, SCRAPER_API, ZENROWS_API
from podimo.utils import (is_correct_email_address, token_key,
                          randomFlyerId, generateHeaders as gHdrs,
                          async_wrap)
from podimo.cache import insertIntoPodcastCache, getCacheEntry, podcast_cache
from time import time
import logging
from typing import Optional, Dict, Any

class PodimoError(Exception):
    """Base exception for Podimo API errors."""
    pass

class PodcastNotFoundError(PodimoError):
    """Raised when a podcast cannot be found."""
    pass

class AuthenticationError(PodimoError):
    """Raised when login credentials are invalid."""
    pass

if ZENROWS_API is not None:
    from zenrows import ZenRowsClient

_zenrows_client: Optional[ZenRowsClient] = None

def _get_zenrows_client() -> Optional[ZenRowsClient]:
    global _zenrows_client
    if _zenrows_client is None and ZENROWS_API is not None:
        _zenrows_client = ZenRowsClient(ZENROWS_API)
    return _zenrows_client

class PodimoClient:
    def __init__(self, username: str, password: str, region: str, locale: str):
        self.username: str = username
        self.password: str = password
        self.region: str = region
        self.locale: str = locale
        self.cookie_jar: Optional[Any] = None
        self.key: str = token_key(username, password)
        self.token: Optional[str] = None
        self.preauth_token: Optional[str] = None
        self.prereg_id: Optional[str] = None

        if len(self.username) == 0 or len(self.password) == 0:
            raise ValueError("Empty username or password")
        if len(self.username) > 256 or len(self.password) > 256:
            raise ValueError("Username or password are too long")
        if not is_correct_email_address(username):
            raise ValueError("Email is not in the correct format")

    def generateHeaders(self, authorization: Optional[str]) -> Dict[str, str]:
        return gHdrs(authorization, self.locale)

    async def post(self, headers: Dict[str, str], query: str, variables: Dict[str, Any], scraper: Any) -> Dict[str, Any]:
        if SCRAPER_API is not None:
            POST_URL = f"https://api.scraperapi.com?api_key={SCRAPER_API}&url={GRAPHQL_URL}&keep_headers=true"
        elif ZENROWS_API is not None:
            POST_URL = GRAPHQL_URL
            scraper = _get_zenrows_client()
        else:
            POST_URL = GRAPHQL_URL
        response = await async_wrap(scraper.post)(POST_URL,
                                        headers=headers,
                                        cookies=self.cookie_jar,
                                        json={"query": query, "variables": variables},
                                        timeout=(6.05, 30)
                                    )
        if response is None:
            raise RuntimeError(f"Could not receive response for query: {query.strip()[:30]}...")
        if response.status_code != 200:
            raise RuntimeError(f"Podimo returned an error code. Response code was: {response.status_code} for query \"{query.strip()[:30]}...\"")
        result = response.json()["data"]
        if result is None:
            raise RuntimeError(f"Podimo returned no valid data for query {query.strip()[:30]}")
        return result

    # This gets the authentication token that is required for subsequent requests
    # as an anonymous user
    async def getPreregisterToken(self, scraper: Any) -> str:
        headers = self.generateHeaders(None)
        logging.debug("AuthorizationPreregisterUser")
        query = """
            query AuthorizationPreregisterUser($locale: String!, $referenceUser: String, $countryCode: String, $appsFlyerId: String) {
                tokenWithPreregisterUser(
                    locale: $locale
                    referenceUser: $referenceUser
                    countryCode: $countryCode
                    source: MOBILE
                    appsFlyerId: $appsFlyerId
                    currentCountry: $countryCode
                ) {
                    token
                }
            }
        """
        variables = {"locale": self.locale, "countryCode": self.region, "appsFlyerId": randomFlyerId()}
        result = await self.post(headers, query, variables, scraper)
        tokenWithPreregisterUser = result["tokenWithPreregisterUser"]
        if not tokenWithPreregisterUser:
            raise RuntimeError("Podimo did not provide a tokenWithPreregisterUser")
        self.preauth_token = result["tokenWithPreregisterUser"]["token"]
        if not self.preauth_token:
            raise RuntimeError("Podimo did not provide a tokenWithPreregisterUser token")
        return self.preauth_token


    async def getOnboardingId(self, scraper: Any) -> str:
        headers = self.generateHeaders(self.preauth_token)
        logging.debug("OnboardingQuery")
        query = """
            query OnboardingQuery {
                userOnboardingFlow {
                    id
                }
            }
        """
        variables = {"locale": self.locale, "countryCode": self.region, "appsFlyerId": randomFlyerId()}
        result = await self.post(headers, query, variables, scraper)
        self.prereg_id = result["userOnboardingFlow"]["id"]
        return self.prereg_id


    async def podimoLogin(self, scraper: Any) -> str:
            await self.getPreregisterToken(scraper)
            await self.getOnboardingId(scraper)

            headers = self.generateHeaders(self.preauth_token)
            logging.debug(f"AuthorizationAuthorize user: {self.username}")
            query = """
                query AuthorizationAuthorize($email: String!, $password: String!, $locale: String!, $preregisterId: String) {
                    tokenWithCredentials(
                    email: $email
                    password: $password
                    locale: $locale
                    preregisterId: $preregisterId
                ) {
                    token
                    }
                }
            """
            variables = {
                "email": self.username,
                "password": self.password,
                "locale": self.locale,
                "preregisterId": self.prereg_id,
            }
            result = await self.post(headers, query, variables, scraper)
            tokenWithCredentials = result["tokenWithCredentials"]
            if not tokenWithCredentials:
                raise AuthenticationError("Invalid Podimo credentials, did not receive tokenWithCredentials")

            self.token = result["tokenWithCredentials"]["token"]
            if self.token:
                return self.token
            else:
                raise AuthenticationError("Invalid Podimo credentials, did not receive token")

    async def getPodcasts(self, podcast_id: str, scraper: Any) -> Dict[str, Any]:
        podcast = getCacheEntry(podcast_id, podcast_cache)
        if podcast:
            timestamp, _ = podcast_cache[podcast_id]
            podcastName = self.getPodcastName(podcast)
            logging.debug(f"Got podcast '{podcastName}' ({podcast_id}) from cache ({int(timestamp-time())} seconds left)")
            return podcast

        headers = self.generateHeaders(self.token)
        logging.debug("ChannelEpisodesQuery")
        query = """
            query ChannelEpisodesQuery($podcastId: String!, $limit: Int!, $offset: Int!, $sorting: PodcastEpisodeSorting) {
                episodes: podcastEpisodes(
                podcastId: $podcastId
                converted: true
                published: true
                limit: $limit
                offset: $offset
                sorting: $sorting
                ) {
                ...EpisodeBase
                }
                podcast: podcastById(podcastId: $podcastId) {
                title
                description
                webAddress
                authorName
                language
                images {
                    coverImageUrl
                }
                }
            }

            fragment EpisodeBase on PodcastEpisode {
                id
                artist
                podcastName
                imageUrl
                description
                datetime
                publishDatetime
                title
                audio {
                url
                duration
                }
                streamMedia {
                duration
                url
                }
            }
        """

        limit = 100
        offset = 0
        fullResult: Optional[Dict[str, Any]] = None
        while True:
            variables = {
                "podcastId": podcast_id,
                "limit": limit,
                "offset": offset,
                "sorting": "PUBLISHED_DESCENDING",
            }
            result = await self.post(headers, query, variables, scraper)
            if offset == 0:
                podcastName = self.getPodcastName(result)
                logging.debug(f"Fetched podcast '{podcastName}' ({podcast_id}) directly")
                fullResult = result
            else:
                if fullResult is not None:
                    fullResult["episodes"] += result["episodes"]
            numEpisodes = len(result["episodes"])
            if numEpisodes == limit:
                logging.debug(f"Fetched {numEpisodes} episodes; fetching more...")
                offset += limit
            else:
                logging.debug(f"Fetched {numEpisodes} episodes; no more to fetch")
                break
        
        if fullResult is not None:
            insertIntoPodcastCache(podcast_id, fullResult)
            return fullResult
        else:
            raise PodcastNotFoundError(f"Podcast {podcast_id} not found or empty response")

    def getPodcastName(self, podcast: Dict[str, Any]) -> str:
        return podcast.get("podcast", {}).get("title", "Unknown")
       
