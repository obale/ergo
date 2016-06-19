// Copyright (c) 2012-2014 Jeremy Latt
// Copyright (c) 2014-2015 Edmund Huber
// Copyright (c) 2016- Daniel Oaks <daniel@danieloaks.net>
// released under the MIT license

package irc

import (
	"fmt"
	"log"
	"strconv"
)

type Channel struct {
	flags      ChannelModeSet
	lists      map[ChannelMode]*UserMaskSet
	key        string
	members    MemberSet
	name       Name
	nameString string
	server     *Server
	topic      string
	userLimit  uint64
}

// NewChannel creates a new channel from a `Server` and a `name`
// string, which must be unique on the server.
func NewChannel(s *Server, name Name, addDefaultModes bool) *Channel {
	channel := &Channel{
		flags: make(ChannelModeSet),
		lists: map[ChannelMode]*UserMaskSet{
			BanMask:    NewUserMaskSet(),
			ExceptMask: NewUserMaskSet(),
			InviteMask: NewUserMaskSet(),
		},
		members:    make(MemberSet),
		name:       name,
		nameString: name.String(),
		server:     s,
	}

	if addDefaultModes {
		for _, mode := range DefaultChannelModes {
			channel.flags[mode] = true
		}
	}

	s.channels.Add(channel)

	return channel
}

func (channel *Channel) IsEmpty() bool {
	return len(channel.members) == 0
}

func (channel *Channel) Names(client *Client) {
	currentNicks := channel.Nicks(client)
	// assemble and send replies
	maxNamLen := 480 - len(client.server.name) - len(client.nickString)
	var buffer string
	for _, nick := range currentNicks {
		if buffer == "" {
			buffer += nick
			continue
		}

		if len(buffer)+1+len(nick) > maxNamLen {
			client.Send(nil, client.server.name, RPL_NAMREPLY, "=", channel.nameString, buffer)
			buffer = nick
			continue
		}

		buffer += " "
		buffer += nick
	}

	client.Send(nil, client.server.name, RPL_NAMREPLY, "=", channel.nameString, buffer)
	client.Send(nil, client.server.name, RPL_ENDOFNAMES, channel.nameString, "End of NAMES list")
}

func (channel *Channel) ClientIsOperator(client *Client) bool {
	return client.flags[Operator] || channel.members.HasMode(client, ChannelOperator)
}

// Prefixes returns a list of prefixes for the given set of channel modes.
func (modes ChannelModeSet) Prefixes(isMultiPrefix bool) string {
	var prefixes string

	// add prefixes in order from highest to lowest privs
	for _, mode := range ChannelPrivModes {
		if modes[mode] {
			prefixes += ChannelModePrefixes[mode]
		}
	}
	if modes[Voice] {
		prefixes += ChannelModePrefixes[Voice]
	}

	if !isMultiPrefix && len(prefixes) > 1 {
		prefixes = string(prefixes[0])
	}

	return prefixes
}

func (channel *Channel) Nicks(target *Client) []string {
	isMultiPrefix := (target != nil) && target.capabilities[MultiPrefix]
	nicks := make([]string, len(channel.members))
	i := 0
	for client, modes := range channel.members {
		nicks[i] += modes.Prefixes(isMultiPrefix)
		nicks[i] += client.Nick().String()
		i += 1
	}
	return nicks
}

func (channel *Channel) Id() Name {
	return channel.name
}

func (channel *Channel) Nick() Name {
	return channel.name
}

func (channel *Channel) String() string {
	return channel.Id().String()
}

// <mode> <mode params>
func (channel *Channel) ModeString(client *Client) (str string) {
	isMember := client.flags[Operator] || channel.members.Has(client)
	showKey := isMember && (channel.key != "")
	showUserLimit := channel.userLimit > 0

	// flags with args
	if showKey {
		str += Key.String()
	}
	if showUserLimit {
		str += UserLimit.String()
	}

	// flags
	for mode := range channel.flags {
		str += mode.String()
	}

	str = "+" + str

	// args for flags with args: The order must match above to keep
	// positional arguments in place.
	if showKey {
		str += " " + channel.key
	}
	if showUserLimit {
		str += " " + strconv.FormatUint(channel.userLimit, 10)
	}

	return
}

func (channel *Channel) IsFull() bool {
	return (channel.userLimit > 0) &&
		(uint64(len(channel.members)) >= channel.userLimit)
}

func (channel *Channel) CheckKey(key string) bool {
	return (channel.key == "") || (channel.key == key)
}

func (channel *Channel) Join(client *Client, key string) {
	if channel.members.Has(client) {
		// already joined, no message?
		return
	}

	if channel.IsFull() {
		client.Send(nil, client.server.name, ERR_CHANNELISFULL, channel.nameString, "Cannot join channel (+l)")
		return
	}

	if !channel.CheckKey(key) {
		client.Send(nil, client.server.name, ERR_BADCHANNELKEY, channel.nameString, "Cannot join channel (+k)")
		return
	}

	isInvited := channel.lists[InviteMask].Match(client.UserHost())
	if channel.flags[InviteOnly] && !isInvited {
		client.Send(nil, client.server.name, ERR_INVITEONLYCHAN, channel.nameString, "Cannot join channel (+i)")
		return
	}

	if channel.lists[BanMask].Match(client.UserHost()) &&
		!isInvited &&
		!channel.lists[ExceptMask].Match(client.UserHost()) {
		client.Send(nil, client.server.name, ERR_BANNEDFROMCHAN, channel.nameString, "Cannot join channel (+b)")
		return
	}

	client.channels.Add(channel)
	channel.members.Add(client)
	if !channel.flags[Persistent] && (len(channel.members) == 1) {
		channel.members[client][ChannelFounder] = true
		channel.members[client][ChannelOperator] = true
	}

	client.Send(nil, client.nickMaskString, "JOIN", channel.nameString)
	return
	//TODO(dan): should we be continuing here????
	// return was above this originally, is it required?
	/*
		for member := range channel.members {
			member.Reply(reply)
		}
		channel.GetTopic(client)
		channel.Names(client)
	*/
}

func (channel *Channel) Part(client *Client, message string) {
	if !channel.members.Has(client) {
		client.Send(nil, client.server.name, ERR_NOTONCHANNEL, channel.nameString, "You're not on that channel")
		return
	}

	for member := range channel.members {
		member.Send(nil, client.nickMaskString, "PART", channel.nameString, message)
	}
	channel.Quit(client)
}

func (channel *Channel) GetTopic(client *Client) {
	if !channel.members.Has(client) {
		client.Send(nil, client.server.name, ERR_NOTONCHANNEL, channel.nameString, "You're not on that channel")
		return
	}

	if channel.topic == "" {
		// clients appear not to expect this
		//replier.Reply(RplNoTopic(channel))
		return
	}

	client.Send(nil, client.server.name, RPL_TOPIC, channel.nameString, channel.topic)
}

func (channel *Channel) SetTopic(client *Client, topic string) {
	if !(client.flags[Operator] || channel.members.Has(client)) {
		client.Send(nil, client.server.name, ERR_NOTONCHANNEL, channel.nameString, "You're not on that channel")
		return
	}

	if channel.flags[OpOnlyTopic] && !channel.ClientIsOperator(client) {
		client.Send(nil, client.server.name, ERR_CHANOPRIVSNEEDED, channel.nameString, "You're not a channel operator")
		return
	}

	channel.topic = topic

	for member := range channel.members {
		member.Send(nil, client.nickMaskString, "TOPIC", channel.nameString, channel.topic)
	}

	if err := channel.Persist(); err != nil {
		log.Println("Channel.Persist:", channel, err)
	}
}

func (channel *Channel) CanSpeak(client *Client) bool {
	if client.flags[Operator] {
		return true
	}
	if channel.flags[NoOutside] && !channel.members.Has(client) {
		return false
	}
	if channel.flags[Moderated] && !(channel.members.HasMode(client, Voice) ||
		channel.members.HasMode(client, ChannelOperator)) {
		return false
	}
	return true
}

func (channel *Channel) PrivMsg(client *Client, message string) {
	if !channel.CanSpeak(client) {
		client.Send(nil, client.server.name, ERR_CANNOTSENDTOCHAN, channel.nameString, "Cannot send to channel")
		return
	}
	for member := range channel.members {
		if member == client {
			continue
		}
		//TODO(dan): use nickmask instead of nickString here lel
		member.Send(nil, client.nickMaskString, "PRIVMSG", channel.nameString, message)
	}
}

func (channel *Channel) applyModeFlag(client *Client, mode ChannelMode,
	op ModeOp) bool {
	if !channel.ClientIsOperator(client) {
		client.Send(nil, client.server.name, ERR_CHANOPRIVSNEEDED, channel.nameString, "You're not a channel operator")
		return false
	}

	switch op {
	case Add:
		if channel.flags[mode] {
			return false
		}
		channel.flags[mode] = true
		return true

	case Remove:
		if !channel.flags[mode] {
			return false
		}
		delete(channel.flags, mode)
		return true
	}
	return false
}

func (channel *Channel) applyModeMember(client *Client, mode ChannelMode,
	op ModeOp, nick string) bool {
	if !channel.ClientIsOperator(client) {
		client.Send(nil, client.server.name, ERR_CHANOPRIVSNEEDED, channel.nameString, "You're not a channel operator")
		return false
	}

	if nick == "" {
		//TODO(dan): shouldn't this be handled before it reaches this function?
		client.Send(nil, client.server.name, ERR_NEEDMOREPARAMS, "MODE", "Not enough parameters")
		return false
	}

	target := channel.server.clients.Get(Name(nick))
	if target == nil {
		//TODO(dan): investigate using NOSUCHNICK and NOSUCHCHANNEL specifically as that other IRCd (insp?) does,
		// since I think that would make sense
		client.Send(nil, client.server.name, ERR_NOSUCHNICK, nick, "No such nick/channel")
		return false
	}

	if !channel.members.Has(target) {
		client.Send(nil, client.server.name, ERR_USERNOTINCHANNEL, client.nickString, channel.nameString, "They aren't on that channel")
		return false
	}

	switch op {
	case Add:
		if channel.members[target][mode] {
			return false
		}
		channel.members[target][mode] = true
		return true

	case Remove:
		if !channel.members[target][mode] {
			return false
		}
		channel.members[target][mode] = false
		return true
	}
	return false
}

func (channel *Channel) ShowMaskList(client *Client, mode ChannelMode) {
	//TODO(dan): WE NEED TO fiX this PROPERLY
	log.Fatal("Implement ShowMaskList")
	/*
		for lmask := range channel.lists[mode].masks {
			client.RplMaskList(mode, channel, lmask)
		}
		client.RplEndOfMaskList(mode, channel)*/
}

func (channel *Channel) applyModeMask(client *Client, mode ChannelMode, op ModeOp,
	mask Name) bool {
	list := channel.lists[mode]
	if list == nil {
		// This should never happen, but better safe than panicky.
		return false
	}

	if (op == List) || (mask == "") {
		channel.ShowMaskList(client, mode)
		return false
	}

	if !channel.ClientIsOperator(client) {
		client.Send(nil, client.server.name, ERR_CHANOPRIVSNEEDED, channel.nameString, "You're not a channel operator")
		return false
	}

	if op == Add {
		return list.Add(mask)
	}

	if op == Remove {
		return list.Remove(mask)
	}

	return false
}

func (channel *Channel) applyMode(client *Client, change *ChannelModeChange) bool {
	switch change.mode {
	case BanMask, ExceptMask, InviteMask:
		return channel.applyModeMask(client, change.mode, change.op,
			NewName(change.arg))

	case InviteOnly, Moderated, NoOutside, OpOnlyTopic, Persistent, Secret:
		return channel.applyModeFlag(client, change.mode, change.op)

	case Key:
		if !channel.ClientIsOperator(client) {
			client.Send(nil, client.server.name, ERR_CHANOPRIVSNEEDED, channel.nameString, "You're not a channel operator")
			return false
		}

		switch change.op {
		case Add:
			if change.arg == "" {
				client.Send(nil, client.server.name, ERR_NEEDMOREPARAMS, "MODE", "Not enough parameters")
				return false
			}
			key := change.arg
			if key == channel.key {
				return false
			}

			channel.key = key
			return true

		case Remove:
			channel.key = ""
			return true
		}

	case UserLimit:
		limit, err := strconv.ParseUint(change.arg, 10, 64)
		if err != nil {
			client.Send(nil, client.server.name, ERR_NEEDMOREPARAMS, "MODE", "Not enough parameters")
			return false
		}
		if (limit == 0) || (limit == channel.userLimit) {
			return false
		}

		channel.userLimit = limit
		return true

	case ChannelFounder, ChannelAdmin, ChannelOperator, Halfop, Voice:
		var hasPrivs bool

		// make sure client has privs to edit the given prefix
		for _, mode := range ChannelPrivModes {
			if channel.members[client][mode] {
				hasPrivs = true

				// Admins can't give other people Admin or remove it from others,
				// standard for that channel mode, we worry about this later
				if mode == ChannelAdmin && change.mode == ChannelAdmin {
					hasPrivs = false
				}

				break
			} else if mode == change.mode {
				break
			}
		}

		name := NewName(change.arg)

		if !hasPrivs {
			if change.op == Remove && name.ToLower() == client.nick.ToLower() {
				// success!
			} else {
				client.Send(nil, client.server.name, ERR_CHANOPRIVSNEEDED, channel.nameString, "You're not a channel operator")
				return false
			}
		}

		return channel.applyModeMember(client, change.mode, change.op, name.String())

	default:
		client.Send(nil, client.server.name, ERR_UNKNOWNMODE, change.mode.String(), fmt.Sprintf(":is an unknown mode char to me for %s", channel))
	}
	return false
}

func (channel *Channel) Mode(client *Client, changes ChannelModeChanges) {
	if len(changes) == 0 {
		client.Send(nil, client.server.name, RPL_CHANNELMODEIS, channel.nameString, channel.ModeString(client))
		return
	}

	applied := make(ChannelModeChanges, 0)
	for _, change := range changes {
		if channel.applyMode(client, change) {
			applied = append(applied, change)
		}
	}

	if len(applied) > 0 {
		appliedString := applied.String()
		for member := range channel.members {
			member.Send(nil, client.nickMaskString, "MODE", channel.nameString, appliedString)
		}

		if err := channel.Persist(); err != nil {
			log.Println("Channel.Persist:", channel, err)
		}
	}
}

func (channel *Channel) Persist() (err error) {
	if channel.flags[Persistent] {
		_, err = channel.server.db.Exec(`
            INSERT OR REPLACE INTO channel
              (name, flags, key, topic, user_limit, ban_list, except_list,
               invite_list)
              VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			channel.name.String(), channel.flags.String(), channel.key,
			channel.topic, channel.userLimit, channel.lists[BanMask].String(),
			channel.lists[ExceptMask].String(), channel.lists[InviteMask].String())
	} else {
		_, err = channel.server.db.Exec(`
            DELETE FROM channel WHERE name = ?`, channel.name.String())
	}
	return
}

func (channel *Channel) Notice(client *Client, message string) {
	if !channel.CanSpeak(client) {
		client.Send(nil, client.server.name, ERR_CANNOTSENDTOCHAN, channel.nameString, "Cannot send to channel")
		return
	}
	for member := range channel.members {
		if member == client {
			continue
		}
		member.Send(nil, client.nickMaskString, "NOTICE", channel.nameString, message)
	}
}

func (channel *Channel) Quit(client *Client) {
	channel.members.Remove(client)
	client.channels.Remove(channel)

	if !channel.flags[Persistent] && channel.IsEmpty() {
		channel.server.channels.Remove(channel)
	}
}

func (channel *Channel) Kick(client *Client, target *Client, comment string) {
	if !(client.flags[Operator] || channel.members.Has(client)) {
		client.Send(nil, client.server.name, ERR_NOTONCHANNEL, channel.nameString, "You're not on that channel")
		return
	}
	if !channel.ClientIsOperator(client) {
		client.Send(nil, client.server.name, ERR_CANNOTSENDTOCHAN, channel.nameString, "Cannot send to channel")
		return
	}
	if !channel.members.Has(target) {
		client.Send(nil, client.server.name, ERR_USERNOTINCHANNEL, client.nickString, channel.nameString, "They aren't on that channel")
		return
	}

	for member := range channel.members {
		member.Send(nil, client.nickMaskString, "KICK", channel.nameString, target.nickString, comment)
	}
	channel.Quit(target)
}

func (channel *Channel) Invite(invitee *Client, inviter *Client) {
	if channel.flags[InviteOnly] && !channel.ClientIsOperator(inviter) {
		inviter.Send(nil, inviter.server.name, ERR_CHANOPRIVSNEEDED, channel.nameString, "You're not a channel operator")
		return
	}

	if !channel.members.Has(inviter) {
		inviter.Send(nil, inviter.server.name, ERR_NOTONCHANNEL, channel.nameString, "You're not on that channel")
		return
	}

	if channel.flags[InviteOnly] {
		channel.lists[InviteMask].Add(invitee.UserHost())
		if err := channel.Persist(); err != nil {
			log.Println("Channel.Persist:", channel, err)
		}
	}

	//TODO(dan): should inviter.server.name here be inviter.nickMaskString ?
	inviter.Send(nil, inviter.server.name, RPL_INVITING, invitee.nickString, channel.nameString)
	invitee.Send(nil, inviter.nickMaskString, "INVITE", invitee.nickString, channel.nameString)
	if invitee.flags[Away] {
		inviter.Send(nil, inviter.server.name, RPL_AWAY, invitee.nickString, invitee.awayMessage)
	}
}
