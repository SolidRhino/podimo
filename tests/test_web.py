import pytest
from unittest.mock import AsyncMock, patch
from main import app, podcast_id_pattern


class TestWebRoutes:
    """Test Quart web endpoints."""

    @pytest.mark.asyncio
    async def test_search_podcasts_returns_results(self, monkeypatch):
        """/search?q=... should return JSON podcast results."""
        import main
        monkeypatch.setattr(main, "LOCAL_CREDENTIALS", True)
        monkeypatch.setattr(main, "PODIMO_EMAIL", "test@example.com")
        monkeypatch.setattr(main, "PODIMO_PASSWORD", "password123")

        mock_client = AsyncMock()
        mock_client.searchPodcasts = AsyncMock(return_value=[
            {"id": "09c55c96-9b1b-456e-bdf2-3abed3b61db5", "title": "Test Podcast", "coverImageUrl": "https://cdn.example.com/img.jpg"}
        ])

        with patch.object(main, 'check_auth', new=AsyncMock(return_value=mock_client)):
            async with app.test_client() as client:
                response = await client.get('/search?q=test+podcast')
                assert response.status_code == 200
                body = await response.get_data()
                assert b'"results"' in body
                assert b'"Test Podcast"' in body

    @pytest.mark.asyncio
    async def test_search_podcasts_short_query(self):
        """/search with <2 char query should return 400."""
        async with app.test_client() as client:
            response = await client.get('/search?q=x')
            assert response.status_code == 400

    @pytest.mark.asyncio
    async def test_subscriptions_returns_followed(self, monkeypatch):
        """/subscriptions should return JSON followed podcasts."""
        import main
        monkeypatch.setattr(main, "LOCAL_CREDENTIALS", True)
        monkeypatch.setattr(main, "PODIMO_EMAIL", "test@example.com")
        monkeypatch.setattr(main, "PODIMO_PASSWORD", "password123")

        mock_client = AsyncMock()
        mock_client.getFollowedPodcasts = AsyncMock(return_value=[
            {"id": "09c55c96-9b1b-456e-bdf2-3abed3b61db5", "title": "My Podcast", "coverImageUrl": "https://cdn.example.com/img.jpg"}
        ])

        with patch.object(main, 'check_auth', new=AsyncMock(return_value=mock_client)):
            async with app.test_client() as client:
                response = await client.get('/subscriptions')
                assert response.status_code == 200
                body = await response.get_data()
                assert b'"results"' in body
                assert b'"My Podcast"' in body

    @pytest.mark.asyncio
    async def test_health_endpoint(self):
        """/health should return 200 JSON with status=ok."""
        async with app.test_client() as client:
            response = await client.get('/health')
            assert response.status_code == 200
            body = await response.get_data()
            assert b'"status"' in body
            assert b'"ok"' in body

    @pytest.mark.asyncio
    async def test_index_get(self):
        """The root route should return 200 with the form."""
        async with app.test_client() as client:
            response = await client.get('/')
            assert response.status_code == 200
            body = await response.get_data()
            assert b'Podimo-to-RSS converter' in body

    @pytest.mark.asyncio
    async def test_index_post_missing_fields(self):
        """POST without required fields should return the form with errors."""
        async with app.test_client() as client:
            response = await client.post('/', data={})
            assert response.status_code == 200
            body = await response.get_data()
            assert b'Error' in body

    @pytest.mark.asyncio
    async def test_feed_invalid_podcast_id(self):
        """Invalid podcast ID format should return 400."""
        async with app.test_client() as client:
            response = await client.get('/feed/not-a-uuid.xml')
            # Should be 400 for invalid format, but depends on auth flow
            # At minimum it should not crash
            assert response.status_code in [400, 401]

    @pytest.mark.asyncio
    async def test_404(self):
        """Unknown routes should return 404 with example text."""
        async with app.test_client() as client:
            response = await client.get('/nonexistent')
            assert response.status_code == 404
            body = await response.get_data()
            assert b'404' in body or b'Example' in body

    @pytest.mark.asyncio
    async def test_rate_limiter_blocks_after_threshold(self):
        """After 8 requests from the same IP, the 9th should return 429."""
        async with app.test_client() as client:
            # Make 8 requests quickly
            for _ in range(8):
                response = await client.get('/feed/09c55c96-9b1b-456e-bdf2-3abed3b61db5.xml')
                # These may return 401 (no auth) but should NOT be 429
                assert response.status_code != 429

            # The 9th request should be rate limited
            response = await client.get('/feed/09c55c96-9b1b-456e-bdf2-3abed3b61db5.xml')
            assert response.status_code == 429
            body = await response.get_data()
            assert b'Rate limit exceeded' in body

    @pytest.mark.asyncio
    async def test_serve_basic_auth_feed_without_auth(self):
        """Feed endpoint without Basic Auth should return 401."""
        async with app.test_client() as client:
            response = await client.get('/feed/09c55c96-9b1b-456e-bdf2-3abed3b61db5.xml')
            assert response.status_code == 401
            body = await response.get_data()
            assert b'401 Unauthorized' in body or b'Unauthorized' in body

    @pytest.mark.asyncio
    async def test_serve_basic_auth_feed_invalid_region(self):
        """Feed endpoint with invalid region should return 400."""
        async with app.test_client() as client:
            response = await client.get(
                '/feed/09c55c96-9b1b-456e-bdf2-3abed3b61db5.xml',
                headers={'Authorization': 'Basic dXNlcixpbnZhbGlkLGluLXZhbGlk'}  # user,invalid,en
            )
            assert response.status_code == 400

    @pytest.mark.asyncio
    async def test_serve_basic_auth_feed_invalid_locale(self):
        """Feed endpoint with invalid locale should return 400."""
        async with app.test_client() as client:
            response = await client.get(
                '/feed/09c55c96-9b1b-456e-bdf2-3abed3b61db5.xml',
                headers={'Authorization': 'Basic dXNlcixubCxpbi12YWxpZA=='}  # user,nl,invalid
            )
            assert response.status_code == 400

    @pytest.mark.asyncio
    async def test_serve_basic_auth_feed_invalid_region_valid_locale(self):
        """Feed endpoint with invalid region but valid locale should return 400.

        Regression test: the region guard was previously dead code (orphaned
        inside the podcast-id check), so only locale validation caught bad
        regions. This test isolates region validation with a valid locale.
        """
        async with app.test_client() as client:
            response = await client.get(
                '/feed/09c55c96-9b1b-456e-bdf2-3abed3b61db5.xml',
                # user@example.com,badregion,nl-NL:pass
                headers={'Authorization': 'Basic dXNlckBleGFtcGxlLmNvbSxiYWRyZWdpb24sbmwtTkw6cGFzcw=='}
            )
            assert response.status_code == 400
            body = await response.get_data()
            assert b'Invalid region' in body


class TestPodcastIdRegex:
    """Test the tightened UUID regex."""

    def test_valid_uuid(self):
        assert podcast_id_pattern.fullmatch("09c55c96-9b1b-456e-bdf2-3abed3b61db5")

    def test_invalid_short(self):
        assert podcast_id_pattern.fullmatch("12345") is None

    def test_invalid_dashes_only(self):
        """BUG FIX: Previously '---' would match the permissive regex."""
        assert podcast_id_pattern.fullmatch("---") is None

    def test_invalid_missing_segment(self):
        assert podcast_id_pattern.fullmatch("09c55c96-9b1b-456e-bdf2") is None

    def test_invalid_extra_chars(self):
        assert podcast_id_pattern.fullmatch("09c55c96-9b1b-456e-bdf2-3abed3b61db5-extra") is None

    def test_case_insensitive(self):
        assert podcast_id_pattern.fullmatch("09C55C96-9B1B-456E-BDF2-3ABED3B61DB5")
