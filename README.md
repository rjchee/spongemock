# spongemock
Spongemock is a Slack integration that adds a slash command /spongemock which
will have Spongebob mock the last person who sent a message in the channel.

## Example
![alt text](img/usage.png "Spongebob makes fun of a poor user")

## Setup
First, you want to create a Slack App and create an OAuth Access token with the following permissions:
- chat:write:bot
- channels:history
- groups:history
- im:history
- mpim:history

Then, you can host it on Heroku by clicking the button below.

[![Deploy](https://www.herokucdn.com/deploy/button.png)](https://heroku.com/deploy)

Of course, you can always choose to host it somewhere else. You can run the app with
```bash
go run cmd/spongemock/main.go
```

Spongemock requires the following environmental variables to run:
- AUTHENTICATION_TOKEN: This is your Slack OAuth Access token.
- VERIFICATION_TOKEN: This is your Slack verification token.
- APP_URL: The URL the app is being hosted at.
- PORT: If your app is not being hosted on Heroku, you need to set this to be the port you want the server to be listening to.

Finally, you want to register your deployed app as a slash command on your Slack app. The request URL will be of the form $APP_URL/slack. You also want to escape channels, users, and links sent to your app.

## TODO
- [x] Support referencing a user to mock
- [x] Support supplying a plaintext message to mock
- [x] Support mocking in dms, private channels, etc.
- [ ] Support other messaging platforms like Facebook
- [ ] Add unit tests

