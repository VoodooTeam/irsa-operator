# testing

hack : 
- in order not to miss a resource state with the find function (resources are polled on an `interval` basis), 
- a `testingDelay` variable can be set in each controller so it delays event processing by an amount of time longer than interval (`interval + 100ms`) 
