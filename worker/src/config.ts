import { z } from 'zod';
import { config } from 'dotenv';

config();

export interface WorkerConfig {
    redis: {
        url: string;
        keyTtl: number; // in seconds
        retryAttempts: number;
        retryDelay: number; // in milliseconds
    };
    server: {
        port: number;
        privateHostname?: string | null;
        headless: boolean;
        heartbeatInterval: number; // in milliseconds
    };
    logging: {
        level: 'info' | 'warn' | 'error';
        format: 'json' | 'text';
    };
}

const schema = z.object({
    REDIS_URL: z.url({ message: "Invalid Redis URL" }),
    
    // Time values from env are in seconds
    REDIS_KEY_TTL: z.coerce.number().int().positive().default(60),
    REDIS_RETRY_ATTEMPTS: z.coerce.number().int().positive().default(5),
    REDIS_RETRY_DELAY: z.coerce.number().int().positive().default(3),

    PORT: z.coerce.number().int().positive(),
    PRIVATE_HOSTNAME: z.string().nullish(),
    HEADLESS: z.enum(['true', 'false']).default('true').transform(v => v === 'true'),
    HEARTBEAT_INTERVAL: z.coerce.number().int().positive().default(5),

    LOG_LEVEL: z.enum(['info', 'warn', 'error']).default('info'),
    LOG_FORMAT: z.enum(['json', 'text']).default('json'),
});

let loadedConfig: WorkerConfig | null = null;

export function loadConfig(): WorkerConfig {
    if (loadedConfig) {
        return loadedConfig;
    }
    
    try {
        const parsed = schema.parse(process.env);
        loadedConfig = {
            redis: {
                url: parsed.REDIS_URL,
                keyTtl: parsed.REDIS_KEY_TTL,
                retryAttempts: parsed.REDIS_RETRY_ATTEMPTS,
                retryDelay: parsed.REDIS_RETRY_DELAY * 1000, // Converted to MS for setTimeout
            },
            server: {
                port: parsed.PORT,
                privateHostname: parsed.PRIVATE_HOSTNAME,
                headless: parsed.HEADLESS,
                heartbeatInterval: parsed.HEARTBEAT_INTERVAL * 1000, // Converted to MS for setInterval
            },
            logging: {
                level: parsed.LOG_LEVEL,
                format: parsed.LOG_FORMAT,
            },
        };
        return loadedConfig;
    } catch (error) {
        if (error instanceof z.ZodError) {
            console.error('Configuration validation failed:', error.issues);
            process.exit(1);
        }
        throw error;
    }
} 