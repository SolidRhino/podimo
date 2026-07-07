"""Tests for the cookie stale fix.

Tests cover:
1. Cookie jar type is http.cookiejar.CookieJar
2. Cookie jar clear() works
3. Re-login fallback on non-200 response
"""
import pytest
from unittest.mock import AsyncMock, MagicMock
from http.cookiejar import CookieJar, Cookie
from podimo.client import PodimoClient


class TestCookieJarType:
    """Test that cookie jar uses http.cookiejar.CookieJar."""

    def test_client_cookie_jar_type(self):
        """PodimoClient should use http.cookiejar.CookieJar.

        Note: PodimoClient.__init__() sets cookie_jar to None;
        the cookie jar is assigned by initialize_client() in main.py.
        This test sets it explicitly to verify the type.
        """
        client = PodimoClient("test@example.com", "password123", "nl", "nl-NL")
        client.cookie_jar = CookieJar()
        assert isinstance(client.cookie_jar, CookieJar)

    def test_initialize_client_creates_cookie_jar(self):
        """initialize_client should create a http.cookiejar.CookieJar."""
        import main
        client = main.initialize_client("test@example.com", "password123", "nl", "nl-NL")
        assert isinstance(client.cookie_jar, CookieJar)


class TestCookieJarClear:
    """Test that cookie jar clear() works correctly."""

    def test_cookie_jar_clear_removes_cookies(self):
        """Clearing the cookie jar should remove all cookies."""
        client = PodimoClient("test@example.com", "password123", "nl", "nl-NL")
        client.cookie_jar = CookieJar()
        # Add a cookie to the jar
        c = Cookie(
            version=0,
            name="test_cookie",
            value="test_value",
            port=None,
            port_specified=False,
            domain="podimo.com",
            domain_specified=True,
            domain_initial_dot=False,
            path="/",
            path_specified=True,
            secure=False,
            expires=None,
            discard=True,
            comment=None,
            comment_url=None,
            rest={},
            rfc2109=False,
        )
        client.cookie_jar.set_cookie(c)
        assert len(list(client.cookie_jar)) > 0
        client.cookie_jar.clear()
        assert len(list(client.cookie_jar)) == 0


class TestPostRetryOnFailure:
    """Test that post() retries on non-200 when token is set."""

    @pytest.mark.asyncio
    async def test_post_retries_on_non_200(self):
        """post() should retry once on non-200 when self.token is set."""
        client = PodimoClient("test@example.com", "password123", "nl", "nl-NL")
        client.cookie_jar = CookieJar()
        client.token = "existing-token"

        # Mock the scraper to return non-200 first, then 200
        mock_response_1 = MagicMock()
        mock_response_1.status_code = 500
        mock_response_1.text = "error"
        mock_response_1.json = MagicMock(return_value={"data": {"test": "ok"}})

        mock_response_2 = MagicMock()
        mock_response_2.status_code = 200
        mock_response_2.json = MagicMock(return_value={"data": {"test": "ok"}})

        mock_scraper = MagicMock()
        mock_scraper.post = MagicMock(side_effect=[mock_response_1, mock_response_2])

        # Mock podimoLogin to succeed
        client.podimoLogin = AsyncMock()

        headers = {"test": "header"}
        result = await client.post(headers, "query { test }", {}, mock_scraper, operation_name="TestQuery")

        assert result == {"test": "ok"}
        assert client.podimoLogin.called
        assert mock_scraper.post.call_count == 2

    @pytest.mark.asyncio
    async def test_post_does_not_retry_during_login(self):
        """post() should NOT retry on non-200 when self.token is None (during login)."""
        client = PodimoClient("test@example.com", "password123", "nl", "nl-NL")
        client.cookie_jar = CookieJar()
        client.token = None  # During login flow

        mock_response = MagicMock()
        mock_response.status_code = 500
        mock_response.text = "error"

        mock_scraper = MagicMock()
        mock_scraper.post = MagicMock(return_value=mock_response)

        with pytest.raises(RuntimeError, match="Podimo returned an error code"):
            await client.post({"test": "header"}, "query { test }", {}, mock_scraper, operation_name="TestQuery")

        assert mock_scraper.post.call_count == 1  # No retry

    @pytest.mark.asyncio
    async def test_post_raises_if_retry_also_fails(self):
        """post() should raise if the retry also fails."""
        client = PodimoClient("test@example.com", "password123", "nl", "nl-NL")
        client.cookie_jar = CookieJar()
        client.token = "existing-token"

        mock_response = MagicMock()
        mock_response.status_code = 500
        mock_response.text = "error"

        mock_scraper = MagicMock()
        mock_scraper.post = MagicMock(return_value=mock_response)

        client.podimoLogin = AsyncMock()

        with pytest.raises(RuntimeError, match="Podimo returned an error code"):
            await client.post({"test": "header"}, "query { test }", {}, mock_scraper, operation_name="TestQuery")

        assert client.podimoLogin.called
        assert mock_scraper.post.call_count == 2  # Original + retry
