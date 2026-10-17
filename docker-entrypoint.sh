#!/bin/sh
# YouTube breaks old yt-dlp versions often, so update it on every start.
# A failed update (e.g. no network yet) shouldn't stop the bot.
pip install --quiet --no-cache-dir --upgrade "yt-dlp[default]" || echo "yt-dlp update failed, using installed version"
exec whitmanBot "$@"
