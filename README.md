Spongemock
==========
Spongemock is a collection of services that add Spongebob mocking functionality to a
variety of platforms. Currently, only a Slack integration exists.

Table of Contents
=================

   * [Spongemock](#spongemock)
   * [Setup](#setup)
   * [Slack Integration](#slack-integration)
      * [Example](#example)
      * [Slack Setup](#slack-setup)
   * [TODO](#todo)

Setup
=====
The Spongemock server can be hosted on Heroku by clicking the button below.

[![Deploy](https://www.herokucdn.com/deploy/button.png)](https://heroku.com/deploy)

Of course, you can always choose to host it somewhere else. You can run the app with
```bash
go run cmd/spongemock/main.go
```

Spongemock requires the following environmental variables to run:
- `PORT`: If your app is not being hosted on Heroku, you need to set this to be
  the port you want the server to be listening to.
- `APP_URL`: The URL the app is being hosted at.
- `PLUGINS`: A comma-separated list of the components of Spongemock you want to
  use. Currently, this only includes the Slack plugin. Leaving this variable
  blank means all components will be run.

For setup instructions for the other components, refer to the Setup
instructions below:

* [Slack Setup](#slack-setup)

Slack Integration
=================
The Spongemock Slack integration adds a slash command `/spongemock` which will
have Spongebob mock the last person who sent a message in the channel.

Example
-------
![alt text](img/usage.png "Spongebob makes fun of a poor user")

Slack Setup
-----------
First, you want to create a Slack App and create an OAuth Access token with the
following permissions:
- `chat:write:bot`
- `channels:history`
- `groups:history`
- `im:history`
- `mpim:history`

To run the Slack plugin, the following environmental variables are required:
- `SLACK_OAUTH_TOKEN`: This is your Slack OAuth Access token.
- `SLACK_VERIFICATION_TOKEN`: This is your Slack verification token.

Finally, you want to register your deployed app as a slash command on your
Slack app. The request URL will be of the form `$APP_URL/slack`. You also want
to escape channels, users, and links sent to your app.

TODO
====
- [x] Add Slack support
- [ ] Add Twitter Support
- [ ] Add Facebook Messenger Support
- [ ] Meme with the message inside the picture instead of as regular text on
  the side
- [ ] Add a website/API
- [ ] Add unit tests
