import { chromium, type BrowserServer } from 'playwright';
import { createClient, type RedisClientType } from 'redis';
import { loadConfig } from './config.js';
import type { WorkerConfig } from './config.js';
import { Logger } from './logger.js';


interface WorkerMetadata {
    id: string;
    endpoint: string;
    status: 'available' | 'draining' | 'shutting-down';
    startedAt: number;
    lastHeartbeat: number;
}


class BrowserWorker {
    private readonly workerId: string;
    private readonly config: WorkerConfig;
    private readonly logger: Logger;
    private browserServer: BrowserServer | null = null;

    private readonly redis: RedisClientType;
    private readonly redisSub: RedisClientType;
    private readonly redisKey: string;
    private readonly redisCmdKey: string;

    // State
    private isShuttingDown: boolean = false;
    private isDraining: boolean = false;
    private internalEndpoint: string | null = null;
    private startedAt: number | null = null;

    // Timers
    private heartbeatTimer: NodeJS.Timeout | null = null;
    private drainTimer: NodeJS.Timeout | null = null;
    private drainTimeout: NodeJS.Timeout | null = null;

    constructor() {
        this.workerId = crypto.randomUUID();
        this.config = loadConfig();
        this.logger = new Logger(this.workerId, this.config.logging.level, this.config.logging.format);
        this.redis = createClient({ url: this.config.redis.url });
        this.redisSub = createClient({ url: this.config.redis.url });
        this.redisKey = `worker:${this.workerId}`;
        this.redisCmdKey = `worker:cmd:${this.workerId}`;
    }

    private formatError(error: unknown): Record<string, any> {
        if (error instanceof Error) {
            return { message: error.message, stack: error.stack };
        }
        return { message: String(error) };
    }

    private async connectRedisClient(client: RedisClientType, purpose: string): Promise<void> {
        let attempts = 0;
        this.logger.info(`Connecting to Redis for ${purpose}...`, { url: this.config.redis.url });
        while (attempts < this.config.redis.retryAttempts) {
            try {
                await Promise.race([
                    client.connect(),
                    new Promise((_, reject) =>
                        setTimeout(() => reject(new Error('Redis connection timed out')), 2000)
                    )
                ]);

                await client.ping();

                this.logger.info(`Successfully connected to Redis for ${purpose}`, { url: this.config.redis.url });
                return;
            } catch (error) {
                attempts++;
                this.logger.warn(`Redis connection attempt failed for ${purpose}`, {
                    attempt: attempts,
                    maxAttempts: this.config.redis.retryAttempts,
                    error: this.formatError(error)
                });
                if (attempts >= this.config.redis.retryAttempts) {
                    throw new Error(`Failed to connect to Redis for ${purpose} after ${attempts} attempts.`);
                }
                await new Promise(resolve => setTimeout(resolve, this.config.redis.retryDelay));
            }
        }
    }

    private async listenForCommands(): Promise<void> {
        this.logger.info('Setting up command subscription...', { channel: this.redisCmdKey });
        
        try {
            await this.redisSub.subscribe(this.redisCmdKey, (message) => {
                this.logger.info('Received command via Pub/Sub', { message, channel: this.redisCmdKey });
                
                if (message === 'shutdown') {
                    this.logger.info('Shutdown command received via Pub/Sub. Initiating drain...');
                    this.drainAndShutdown('shutdown_command_pubsub');
                }
            });
            
            this.logger.info('Successfully subscribed to command channel', { channel: this.redisCmdKey });
        } catch (error) {
            this.logger.error('Failed to subscribe to command channel', { 
                channel: this.redisCmdKey, 
                error: this.formatError(error) 
            });
            throw error;
        }
    }

    public async start(): Promise<void> {
        this.logger.info('Starting browser worker...', { config: this.config });

        try {
            await Promise.all([
                this.connectRedisClient(this.redis, 'main'),
                this.connectRedisClient(this.redisSub, 'subscription')
            ]);

            await this.listenForCommands();

            this.browserServer = await chromium.launchServer({
                port: this.config.server.port,
                headless: this.config.server.headless,
                wsPath: `/playwright/${this.workerId}`,
            });

            const wsEndpoint = this.browserServer.wsEndpoint();
            this.internalEndpoint = wsEndpoint;
            if (this.config.server.privateHostname) {
                this.internalEndpoint = wsEndpoint.replace(/ws:\/\/127\.0\.0\.1|ws:\/\/localhost/, `ws://${this.config.server.privateHostname}`);
            }

            this.logger.info('Browser server launched', { endpoint: this.internalEndpoint });

            await this.initializeCounters();
            await this.register();

            this.startHeartbeat();

            process.on('SIGINT', () => this.gracefulShutdown('SIGINT'));
            process.on('SIGTERM', () => this.gracefulShutdown('SIGTERM'));

            this.logger.info('Browser worker is running and registered.');

        } catch (error) {
            this.logger.error('Failed to start browser worker', { error: this.formatError(error) });
            await this.cleanupAndExit(1);
        }
    }

    private async initializeCounters(): Promise<void> {
        this.logger.info('Initializing worker connection counters in Redis...');
        try {
            const [activeResult, lifetimeResult] = await Promise.all([
                this.redis.hSetNX('cluster:active_connections', this.workerId, String(0)),
                this.redis.hSetNX('cluster:lifetime_connections', this.workerId, String(0))
            ]);

            if (activeResult && lifetimeResult) {
                this.logger.info('Successfully initialized connection counters.');
            } else {
                this.logger.info('Connection counters were already initialized for this worker.');
            }
        } catch (error) {
            this.logger.error('Failed to initialize connection counters. Worker will not start.', { error: this.formatError(error) });
            throw error;
        }
    }

    private async register(): Promise<void> {
        if (!this.internalEndpoint) {
            this.logger.error('No endpoint available for registration. Aborting.');
            await this.gracefulShutdown('registration_error_no_endpoint');
            return;
        }

        if (!this.startedAt) {
            this.startedAt = Date.now()
        }

        const metadata: WorkerMetadata = {
            id: this.workerId,
            endpoint: this.internalEndpoint,
            status: 'available',
            startedAt: this.startedAt,
            lastHeartbeat: Date.now()
        };

        await this.redis.hSet(this.redisKey, metadata as unknown as Record<string, string>);
        await this.redis.expire(this.redisKey, this.config.redis.keyTtl);

        this.logger.info('Worker registered in Redis', { key: this.redisKey, endpoint: this.internalEndpoint });
    }

    private startHeartbeat(): void {
        this.heartbeatTimer = setInterval(
            () => this.performHeartbeat(),
            this.config.server.heartbeatInterval
        );
        this.logger.info('Heartbeat started', { intervalMs: this.config.server.heartbeatInterval });
    }

    private async performHeartbeat(): Promise<void> {
        if (this.isShuttingDown) return;

        try {
            const exists = await this.redis.exists(this.redisKey);
            if (!exists) {
                this.logger.warn('Worker key expired. Re-registering...');
                await this.register();
                return;
            }

            await this.redis.hSet(this.redisKey, 'lastHeartbeat', Date.now());
            await this.redis.expire(this.redisKey, this.config.redis.keyTtl);
            this.logger.info('Heartbeat sent', { key: this.redisKey });


            if (this.isDraining) {
                return
            }

            const command = await this.redis.get(this.redisCmdKey);
            if (command === 'shutdown') {
                this.logger.info('Shutdown command received. Initiating drain...');
                await this.redis.del(this.redisCmdKey);
                await this.drainAndShutdown('shutdown_command');
                return;
            }

        } catch (error) {
            this.logger.error('Failed to perform heartbeat', { error: this.formatError(error) });
            await this.gracefulShutdown('heartbeat_error');
        }
    }

    private async drainAndShutdown(initiator: string): Promise<void> {
        if (this.isDraining || this.isShuttingDown) {
            return;
        }

        this.isDraining = true;
        this.logger.info('Starting drain process...', { initiator });

        try {
            await this.redis.hSet(this.redisKey, 'status', 'draining');
        } catch (error) {
            this.logger.error('Failed to update worker status to draining', { error: this.formatError(error) });
        }

        this.drainTimeout = setTimeout(() => {
            this.logger.warn('Drain timeout reached. Forcing shutdown.');
            this.gracefulShutdown('drain_timeout');
        }, 5 * 60 * 1000);

        this.drainTimer = setInterval(async () => {
            try {
                const activeConnections = await this.redis.hGet('cluster:active_connections', this.workerId);
                const count = activeConnections ? parseInt(activeConnections, 10) : 0;

                if (!Number.isFinite(count) || count < 0) {
                    this.logger.warn('Invalid connection count from Redis, treating as 0', { rawValue: activeConnections });
                    if (this.drainTimeout) {
                        clearTimeout(this.drainTimeout);
                        this.drainTimeout = null;
                    }
                    this.logger.info('Invalid connection count detected. Proceeding with shutdown.');
                    await this.gracefulShutdown('drain_invalid_count');
                    return;
                }

                this.logger.info(`Draining... ${count} active connections remaining.`);

                if (count === 0) {
                    if (this.drainTimeout) {
                        clearTimeout(this.drainTimeout);
                        this.drainTimeout = null;
                    }
                    this.logger.info('No active connections remaining. Proceeding with shutdown.');
                    await this.gracefulShutdown('drain_complete');
                }
            } catch (error) {
                this.logger.error('Error checking active connections during drain', { error: this.formatError(error) });
                if (this.drainTimeout) {
                    clearTimeout(this.drainTimeout);
                    this.drainTimeout = null;
                }
                await this.gracefulShutdown('drain_error');
            }
        }, 1000);
    }

    private async gracefulShutdown(initiator: string): Promise<void> {
        if (this.isShuttingDown) return;
        this.isShuttingDown = true;
        this.isDraining = false;

        this.logger.info('Initiating graceful shutdown...', { initiator });

        if (this.heartbeatTimer) {
            clearInterval(this.heartbeatTimer);
            this.heartbeatTimer = null;
        }
        if (this.drainTimer) {
            clearInterval(this.drainTimer);
            this.drainTimer = null;
        }
        if (this.drainTimeout) {
            clearTimeout(this.drainTimeout);
            this.drainTimeout = null;
        }

        try {
            this.logger.info('Updating worker status to "shutting-down" in Redis.');
            const exists = await this.redis.exists(this.redisKey);
            if (exists) {
                await this.redis.hSet(this.redisKey, 'status', 'shutting-down');
                await this.redis.expire(this.redisKey, 10);
            }
        } catch (error) {
            this.logger.error('Failed to update worker status during shutdown.', { error: this.formatError(error) });
        }

        if (this.browserServer) {
            this.logger.info('Closing the browser server.');
            await this.browserServer.close();
            this.logger.info('Browser server closed.');
        }

        await this.cleanupAndExit(0);
    }

    private async cleanupAndExit(exitCode: number): Promise<void> {
        if (this.heartbeatTimer) {
            clearInterval(this.heartbeatTimer);
        }
        if (this.drainTimer) {
            clearInterval(this.drainTimer);
        }
        if (this.drainTimeout) {
            clearTimeout(this.drainTimeout);
        }

        try {
            await this.redis.del(this.redisKey);
            this.logger.info('Worker key removed from Redis.');
        } catch (error) {
            this.logger.error('Failed to remove worker key from Redis during cleanup.', { error: this.formatError(error) });
        }

        try {
            await Promise.all([
                this.redis.quit(),
                this.redisSub.quit()
            ]);
            this.logger.info('Redis connections closed.');
        } catch (error) {
            this.logger.error('Failed to close Redis connections during cleanup.', { error: this.formatError(error) });
        }

        this.logger.info('Exiting.', { exitCode });
        process.exit(exitCode);
    }
}

if (process.argv[1] === new URL(import.meta.url).pathname) {
    const worker = new BrowserWorker();
    worker.start().catch(async (error) => {
        console.error(JSON.stringify({
            timestamp: new Date().toISOString(),
            level: 'error',
            message: 'Caught unhandled exception during startup. Exiting.',
            error: error instanceof Error ? { message: error.message, stack: error.stack } : { message: String(error) }
        }));
        process.exit(1);
    });
} 