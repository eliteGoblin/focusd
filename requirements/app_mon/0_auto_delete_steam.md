 Based on this HLD, I want you implement a layer of auto detect steam if installed, if so. stop it and  delete it. same for dota2. rm -rf
  ~/Library/Application\ Support/Steam/steamapps/common/dota\ 2\ beta/ this is to remove the dota2 installed folder.

  sometimes steam not installed in ~/Library/Application. I usually install steam via brew.  not sure if there's another way prob via dmg to isntall.

  you are currently on ubvuntu, but I want you impelemtn an app for mac. via Python?



  I want basic cli function,

* Check current protect status 
* Start, restart protection: Use 2 process, monitor each other. design and implement for me. If one is killed, another one pull it up or similar. make it resilient. 
* Focus on steam/dota first, for Mac. 
* Remove steam installed via homebrew or dmg, if you can quick serach disk space, if find any dota2 related content, delete it. 
* Only allow start/restart atm, not allow stop. 
* You implement a mechanisim, make even me harder to find the daemon process name or suggest a way to install app (consider it's a Mac, whehther there's some more priviideged way to install, I think I saw some app have to into some rescue mode and uninstall from some module, but for now keep it simple) 
