 Based on this HLD, I want you implement a layer of auto detect steam if installed, if so. stop it and  delete it. same for dota2. rm -rf
  ~/Library/Application\ Support/Steam/steamapps/common/dota\ 2\ beta/ this is to remove the dota2 installed folder.

  sometimes steam not installed in ~/Library/Application. I usually install steam via brew.  not sure if there's another way prob via dmg to isntall.

  you are currently on ubvuntu, but I want you impelemtn an app for mac. via Python?



  I want basic cli function,

* Check current protect status 
* Start, restart protection: Use 2 process, monitor each other. design and implement for me. If one is killed, another one pull it up or similar. make it resilient. 
* make user experience simple: just start/restart, it should check daemon's status and check all process if up and running. 
* Focus on steam/dota first, for Mac. 
* Remove steam installed via homebrew or dmg, if you can quick serach disk space, if find any dota2 related content, delete it. 
* Only allow start/restart atm, not allow stop. 
 * You implement a mechanisim, make even me harder to find the daemon process name or suggest a way to install app (consider it's a Mac, whehther there's some more priviideged way to install, I think I saw some app have to into some rescue mode and uninstall from some module, but for now keep it simple) , you need to hide from me, I mean bit hard for me to find if just read code; but also, you can check daemon's process, so you need to come with a way to actually you know which proces. 

# Non functional 

* structure logging 
* Golang client app first (using hardcoded config in app, later will split as client-server app)
* If no permission, error, just log warning and continue, I don't want app crash because of lack of 
* Make implementation prod ready, using golang cli prod best practice, but KISS
* I wamt you to impment using Clean Architecture, the dependency rule. create model layer and entities etc. then using interface to hide implementation details. isolate each layer using interface. 
* Implement good covereage of unit test , and ideally integration test. setup testing, create folder in certain plance, for example. like using brew to install steam and run cli, shouold detect it in log, and delete and remove the folder. 
* I also want you propose solution for me to setup auto restart even system restard. I mean mac. 
* Implement github action for this golang app. using prod level CI pipeline: format, linting, etc. and running unit test and integration test. I want all pass


# Further requirement

## Resilient && more self-protection 

Just some of my random thought. but essentially., I want to enhace the mon, so won't easily bypass if Im on urge.

another Q: seems key is I need to keep it in LaunchAgent, so it will auto restart on each mac restart? this is key.
  Seeems If I delete the file(I got sudo) and restart, laptop, it will effectively bypass the app?

  another Q:
  what if: I implement some new feature and want to update the client. I need to restart these 2 process as well.
  How can I stop myself as:

  change config, rolling update(assume app itself support) , change config remove steam restriction, then the limit is bypassed?  or I should not allow or should make change config with a lot more
  restriction. So I won't easily change config.

  and config is versioned. app seems should cached last time working config, and keep using this one?


do not use a static place to store the registry or cache, in case I always want to delete the config, or use it to guess the process name. do you have a better solution?
  is there could be some ramdomness to have choose 1 out of like 20, to use one for location? so hard for me to guess. and you just implement some own cache/log to record where's your config will be.
  Just some my thought, just use it as ref. Eseentially, I want you add friction, that I , as dev and access to source code, even I cannot easily findout what's the process, and kill it.

  or delete the config, so it crash when restat. resilient, if config absent, use default one, still always hardcode delete steam, dota2. your own cache/log, should be not readable by me(l;ater we could add
  cloud key to strengthen it, or you suggest me the solution)


