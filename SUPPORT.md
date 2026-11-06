# Getting help with Box Office

## Setting it up

The [README](README.md#run-it) walks through running Box Office with Docker, Docker Compose, Kubernetes or from source, and [moving in](README.md#moving-in-from-radarr-sonarr-lidarr-and-nzbget) from Radarr, Sonarr, Lidarr and NZBGet. The [FAQ](README.md#faq) answers the common questions.

## Connecting Matinee

In Matinee, open **Settings → Add-ons** and click **Connect** next to **Box Office**. Paste the API key from Box Office's **Settings → Connect Matinee**, then **Test** and **Save**. [Matinee's guide](https://github.com/yeixio/matinee/blob/main/docs/guide/server/radarr-and-sonarr.md#box-office) has the details.

## Something's wrong

- **Look at Health first.** The **Check-up** tab names most problems that stop downloads working (a folder it can't write to, a full disk, an indexer or news server that's failing) and says what to do.
- **Search the [issues](https://github.com/yeixio/boxoffice/issues)**: someone may have hit it already.
- **Open a new issue** with what you did, what you expected, what happened, and the version (at the bottom of each page). The **Health → Log** tab can download the log. Leave out API keys, passwords and private addresses.
- **Security problems** go through private reporting instead; see [SECURITY.md](SECURITY.md).

You can also email hello@yeix.io.
